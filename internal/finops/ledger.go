package finops

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/metrics"
	"github.com/divergedev/diverge/internal/multicluster"
)

const (
	// LedgerConfigMapName is the per-namespace ConfigMap storing historical FinOps teardown records.
	LedgerConfigMapName = "diverge-finops-ledger"
	// LedgerDataKey is the ConfigMap data key storing JSON-encoded HistoricalEnvironmentRecord items.
	LedgerDataKey = "records.json"
	// LedgerComponentLabel identifies FinOps ledger ConfigMaps for cluster-wide queries.
	LedgerComponentLabel = "finops-ledger"
	// LedgerRetentionDays bounds how long historical teardown records are retained.
	LedgerRetentionDays = 30
	// LedgerMaxEntries caps records per namespace ConfigMap so etcd object size stays well below 1 MiB.
	LedgerMaxEntries = 500
	// DefaultWindowDays is the default historical aggregation window when window_days is unset.
	DefaultWindowDays = 30
)

// HistoricalEnvironmentRecord captures final cost, lifetime, and savings metrics for a torn-down Environment.
type HistoricalEnvironmentRecord struct {
	UID                    string    `json:"uid,omitempty"`
	Name                   string    `json:"name"`
	Namespace              string    `json:"namespace"`
	Cluster                string    `json:"cluster,omitempty"`
	Project                string    `json:"project,omitempty"`
	MR                     string    `json:"mr,omitempty"`
	Branch                 string    `json:"branch,omitempty"`
	DatabaseMode           string    `json:"databaseMode,omitempty"`
	ServiceCount           int32     `json:"serviceCount"`
	CPUCores               float64   `json:"cpuCores"`
	MemoryGiB              float64   `json:"memoryGiB"`
	CreatedAt              time.Time `json:"createdAt"`
	TerminatedAt           time.Time `json:"terminatedAt"`
	UptimeHours            float64   `json:"uptimeHours"`
	ActiveHours            float64   `json:"activeHours"`
	HibernatedHours        float64   `json:"hibernatedHours"`
	AccruedCostUSD         float64   `json:"accruedCostUsd"`
	HibernationSavingsUSD  float64   `json:"hibernationSavingsUsd"`
	DeltaRoutingSavingsUSD float64   `json:"deltaRoutingSavingsUsd"`
}

func (r HistoricalEnvironmentRecord) dedupKey() string {
	if strings.TrimSpace(r.UID) != "" {
		return "uid:" + strings.TrimSpace(r.UID)
	}
	return fmt.Sprintf("env:%s/%s@%d", r.Namespace, r.Name, r.CreatedAt.UTC().Unix())
}

// NormalizeWindowDays clamps a requested trend window to [1, LedgerRetentionDays], defaulting <=0 to DefaultWindowDays.
func NormalizeWindowDays(windowDays int) int {
	if windowDays <= 0 {
		return DefaultWindowDays
	}
	if windowDays > LedgerRetentionDays {
		return LedgerRetentionDays
	}
	return windowDays
}

// SnapshotEnvironmentTeardown builds a HistoricalEnvironmentRecord from a terminating Environment.
func SnapshotEnvironmentTeardown(env *v1alpha1.Environment, now time.Time, rateCard metrics.CostRateCard, baselineServiceCount int) HistoricalEnvironmentRecord {
	termTime := now.UTC()
	if termTime.IsZero() {
		termTime = time.Now().UTC()
	}

	ns := strings.TrimSpace(env.Namespace)
	if ns == "" {
		ns = "default"
	}

	createdAt := env.CreationTimestamp.UTC()
	if createdAt.IsZero() && env.Status.CreatedAt != nil {
		createdAt = env.Status.CreatedAt.UTC()
	}
	if createdAt.IsZero() || createdAt.After(termTime) {
		createdAt = termTime
	}

	est := metrics.ComputeEnvironmentCost(env, termTime, rateCard)
	activeComputeHourly := est.ComputeHourlyUSD + est.HibernationSavingsHourlyUSD
	hibernationSavingsUSD := 0.0
	if est.HibernatedHours > 0 && activeComputeHourly > 0 {
		hibernationSavingsUSD = est.HibernatedHours * activeComputeHourly
	}

	deltaSavingsUSD := 0.0
	if env.Spec.Deploy.Namespace != "create" && est.ServiceCount > 0 && baselineServiceCount > est.ServiceCount && activeComputeHourly > 0 && est.ActiveHours > 0 {
		perSvcHourly := activeComputeHourly / float64(est.ServiceCount)
		deltaSavingsUSD = est.ActiveHours * float64(baselineServiceCount-est.ServiceCount) * perSvcHourly
	}

	mrStr := ""
	if env.Spec.Source.MR > 0 {
		mrStr = strconv.Itoa(env.Spec.Source.MR)
	} else if lblMR := strings.TrimSpace(env.Labels["divergedev.com/mr"]); lblMR != "" {
		mrStr = lblMR
	}
	project := strings.TrimSpace(env.Spec.Source.Project)
	if project == "" {
		project = strings.TrimSpace(env.Labels["divergedev.com/project"])
	}

	return HistoricalEnvironmentRecord{
		UID:                    string(env.UID),
		Name:                   env.Name,
		Namespace:              ns,
		Cluster:                multicluster.ClusterName(env),
		Project:                project,
		MR:                     mrStr,
		Branch:                 env.Spec.Source.Branch,
		DatabaseMode:           metrics.ResolveEnvironmentDatabaseMode(env),
		ServiceCount:           int32(est.ServiceCount),
		CPUCores:               est.CPUCores,
		MemoryGiB:              est.MemoryGiB,
		CreatedAt:              createdAt,
		TerminatedAt:           termTime,
		UptimeHours:            est.UptimeHours,
		ActiveHours:            est.ActiveHours,
		HibernatedHours:        est.HibernatedHours,
		AccruedCostUSD:         est.AccruedUSD,
		HibernationSavingsUSD:  hibernationSavingsUSD,
		DeltaRoutingSavingsUSD: deltaSavingsUSD,
	}
}

// RecordEnvironmentTeardown persists a torn-down Environment's cost and savings snapshot into
// the namespace's bounded diverge-finops-ledger ConfigMap ring buffer.
func RecordEnvironmentTeardown(ctx context.Context, c client.Client, env *v1alpha1.Environment, now time.Time, rateCard metrics.CostRateCard) error {
	if c == nil || env == nil {
		return nil
	}
	ns := strings.TrimSpace(env.Namespace)
	if ns == "" {
		ns = "default"
	}

	baselineCount := 0
	if raw := strings.TrimSpace(env.Annotations[AnnotationBaselineServiceCount]); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			baselineCount = parsed
		}
	}
	if baselineCount == 0 {
		baselineCount = countBaselineDeployments(ctx, c, nil, ns, 3*time.Second)
	}

	record := SnapshotEnvironmentTeardown(env, now, rateCard, baselineCount)
	cutoff := record.TerminatedAt.AddDate(0, 0, -LedgerRetentionDays)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var cm corev1.ConfigMap
		key := types.NamespacedName{Namespace: ns, Name: LedgerConfigMapName}
		err := c.Get(ctx, key, &cm)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("getting finops ledger configmap %s/%s: %w", ns, LedgerConfigMapName, err)
			}
			encoded, encErr := json.Marshal([]HistoricalEnvironmentRecord{record})
			if encErr != nil {
				return encErr
			}
			createCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      LedgerConfigMapName,
					Namespace: ns,
					Labels: map[string]string{
						"divergedev.com/managed-by": "diverge",
						"divergedev.com/component":  LedgerComponentLabel,
					},
				},
				Data: map[string]string{
					LedgerDataKey: string(encoded),
				},
			}
			if createErr := c.Create(ctx, createCM); createErr != nil {
				if apierrors.IsAlreadyExists(createErr) {
					return apierrors.NewConflict(corev1.Resource("configmaps"), LedgerConfigMapName, createErr)
				}
				return fmt.Errorf("creating finops ledger configmap %s/%s: %w", ns, LedgerConfigMapName, createErr)
			}
			return nil
		}

		existing := parseLedgerConfigMap(&cm)
		merged := mergeAndPruneLedger(existing, record, cutoff, LedgerMaxEntries)
		encoded, encErr := json.Marshal(merged)
		if encErr != nil {
			return encErr
		}
		if cm.Labels == nil {
			cm.Labels = make(map[string]string)
		}
		cm.Labels["divergedev.com/managed-by"] = "diverge"
		cm.Labels["divergedev.com/component"] = LedgerComponentLabel
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data[LedgerDataKey] = string(encoded)
		if updateErr := c.Update(ctx, &cm); updateErr != nil {
			return updateErr
		}
		return nil
	})
}

func parseLedgerConfigMap(cm *corev1.ConfigMap) []HistoricalEnvironmentRecord {
	if cm == nil || len(cm.Data) == 0 {
		return nil
	}
	raw := strings.TrimSpace(cm.Data[LedgerDataKey])
	if raw == "" {
		return nil
	}
	var records []HistoricalEnvironmentRecord
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return nil
	}
	return records
}

func mergeAndPruneLedger(existing []HistoricalEnvironmentRecord, incoming HistoricalEnvironmentRecord, cutoff time.Time, maxEntries int) []HistoricalEnvironmentRecord {
	byKey := make(map[string]HistoricalEnvironmentRecord, len(existing)+1)
	for _, rec := range existing {
		if rec.TerminatedAt.Before(cutoff) {
			continue
		}
		byKey[rec.dedupKey()] = rec
	}
	if !incoming.TerminatedAt.Before(cutoff) {
		byKey[incoming.dedupKey()] = incoming
	}

	out := make([]HistoricalEnvironmentRecord, 0, len(byKey))
	for _, rec := range byKey {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].TerminatedAt.Equal(out[j].TerminatedAt) {
			return out[i].TerminatedAt.Before(out[j].TerminatedAt)
		}
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})

	if maxEntries > 0 && len(out) > maxEntries {
		out = out[len(out)-maxEntries:]
	}
	return out
}

// ListHistoricalRecords loads persisted historical teardown records within the requested UTC day window.
// Missing RBAC permissions or absent ConfigMaps degrade gracefully to an empty slice.
func ListHistoricalRecords(
	ctx context.Context,
	reader client.Reader,
	k8sClient kubernetes.Interface,
	namespace string,
	now time.Time,
	windowDays int,
	timeout time.Duration,
) []HistoricalEnvironmentRecord {
	if reader == nil && k8sClient == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = defaultSummaryTimeout
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	days := NormalizeWindowDays(windowDays)
	windowStart := startOfUTCDay(now).AddDate(0, 0, -(days - 1))

	qCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cms []corev1.ConfigMap
	ns := strings.TrimSpace(namespace)
	if ns != "" {
		if reader != nil {
			var cm corev1.ConfigMap
			if err := reader.Get(qCtx, types.NamespacedName{Namespace: ns, Name: LedgerConfigMapName}, &cm); err == nil {
				cms = append(cms, cm)
			} else if !apierrors.IsNotFound(err) && !apierrors.IsForbidden(err) && !meta.IsNoMatchError(err) && k8sClient != nil {
				if got, kErr := k8sClient.CoreV1().ConfigMaps(ns).Get(qCtx, LedgerConfigMapName, metav1.GetOptions{}); kErr == nil && got != nil {
					cms = append(cms, *got)
				}
			}
		} else if k8sClient != nil {
			if got, err := k8sClient.CoreV1().ConfigMaps(ns).Get(qCtx, LedgerConfigMapName, metav1.GetOptions{}); err == nil && got != nil {
				cms = append(cms, *got)
			}
		}
	} else {
		sel := labels.SelectorFromSet(map[string]string{
			"divergedev.com/component": LedgerComponentLabel,
		})
		if reader != nil {
			var list corev1.ConfigMapList
			if err := reader.List(qCtx, &list, client.MatchingLabelsSelector{Selector: sel}); err == nil {
				cms = append(cms, list.Items...)
			} else if k8sClient != nil {
				if got, kErr := k8sClient.CoreV1().ConfigMaps("").List(qCtx, metav1.ListOptions{LabelSelector: sel.String()}); kErr == nil && got != nil {
					cms = append(cms, got.Items...)
				}
			}
		} else if k8sClient != nil {
			if got, err := k8sClient.CoreV1().ConfigMaps("").List(qCtx, metav1.ListOptions{LabelSelector: sel.String()}); err == nil && got != nil {
				cms = append(cms, got.Items...)
			}
		}
	}

	var filtered []HistoricalEnvironmentRecord
	seen := make(map[string]struct{})
	for i := range cms {
		for _, rec := range parseLedgerConfigMap(&cms[i]) {
			if rec.TerminatedAt.Before(windowStart) || rec.CreatedAt.After(now) {
				continue
			}
			if rec.Cluster == "" {
				rec.Cluster = multicluster.LocalClusterName
			}
			k := rec.dedupKey()
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			filtered = append(filtered, rec)
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		if math.Abs(filtered[i].AccruedCostUSD-filtered[j].AccruedCostUSD) > 1e-9 {
			return filtered[i].AccruedCostUSD > filtered[j].AccruedCostUSD
		}
		if !filtered[i].TerminatedAt.Equal(filtered[j].TerminatedAt) {
			return filtered[i].TerminatedAt.After(filtered[j].TerminatedAt)
		}
		return filtered[i].Name < filtered[j].Name
	})
	return filtered
}

// HistoricalRecordToCostItem converts a persisted ledger record into an EnvironmentCostItem proto.
func HistoricalRecordToCostItem(rec HistoricalEnvironmentRecord) *pb.EnvironmentCostItem {
	cluster := strings.TrimSpace(rec.Cluster)
	if cluster == "" {
		cluster = multicluster.LocalClusterName
	}
	return &pb.EnvironmentCostItem{
		Name:                rec.Name,
		Namespace:           rec.Namespace,
		Project:             rec.Project,
		Mr:                  rec.MR,
		Branch:              rec.Branch,
		Phase:               "Terminated",
		Hibernated:          false,
		HourlyCostUsd:       0,
		AccruedCostUsd:      rec.AccruedCostUSD,
		ProjectedTtlCostUsd: rec.AccruedCostUSD,
		UptimeHours:         rec.UptimeHours,
		CpuCores:            rec.CPUCores,
		MemoryGib:           rec.MemoryGiB,
		ServiceCount:        rec.ServiceCount,
		DatabaseMode:        rec.DatabaseMode,
		CostSource:          CostSourceRateCard,
		Cluster:             cluster,
		Historical:          true,
		TerminatedAt:        rec.TerminatedAt.UTC().Format(time.RFC3339),
	}
}

type trendSpan struct {
	start        time.Time
	end          time.Time
	accruedUSD   float64
	hibSavedUSD  float64
	deltaSaveUSD float64
	terminated   bool
}

func startOfUTCDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

func buildDailyCostTrend(now time.Time, windowDays int, spans []trendSpan) []*pb.DailyCostPoint {
	days := NormalizeWindowDays(windowDays)
	todayStart := startOfUTCDay(now)
	firstDay := todayStart.AddDate(0, 0, -(days - 1))

	points := make([]*pb.DailyCostPoint, days)
	for d := 0; d < days; d++ {
		dayStart := firstDay.AddDate(0, 0, d)
		dayEnd := dayStart.Add(24 * time.Hour)
		effectiveEnd := dayEnd
		if now.Before(effectiveEnd) {
			effectiveEnd = now
		}

		pt := &pb.DailyCostPoint{
			Date: dayStart.Format("2006-01-02"),
		}

		for _, sp := range spans {
			if sp.terminated && !sp.end.Before(dayStart) && sp.end.Before(dayEnd) {
				pt.TerminatedEnvironments++
			}
			if effectiveEnd.Before(dayStart) {
				continue
			}

			totalDur := sp.end.Sub(sp.start)
			if totalDur <= 0 {
				if !sp.start.Before(dayStart) && sp.start.Before(dayEnd) {
					pt.AccruedCostUsd += sp.accruedUSD
					pt.HibernationSavingsUsd += sp.hibSavedUSD
					pt.DeltaRoutingSavingsUsd += sp.deltaSaveUSD
					pt.ActiveEnvironments++
				}
				continue
			}

			ovStart := sp.start
			if dayStart.After(ovStart) {
				ovStart = dayStart
			}
			ovEnd := sp.end
			if effectiveEnd.Before(ovEnd) {
				ovEnd = effectiveEnd
			}
			if !ovEnd.After(ovStart) {
				continue
			}

			frac := ovEnd.Sub(ovStart).Seconds() / totalDur.Seconds()
			pt.AccruedCostUsd += sp.accruedUSD * frac
			pt.HibernationSavingsUsd += sp.hibSavedUSD * frac
			pt.DeltaRoutingSavingsUsd += sp.deltaSaveUSD * frac
			pt.ActiveEnvironments++
		}

		points[d] = pt
	}
	return points
}
