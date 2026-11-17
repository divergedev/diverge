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
	PreviewGroup           string    `json:"previewGroup,omitempty"`
	Project                string    `json:"project,omitempty"`
	MR                     string    `json:"mr,omitempty"`
	Branch                 string    `json:"branch,omitempty"`
	DatabaseMode           string    `json:"databaseMode,omitempty"`
	ServiceCount           int32     `json:"serviceCount"`
	CPUCores               float64   `json:"cpuCores"`
	MemoryGiB              float64   `json:"memoryGiB"`
	ActiveComputeHourlyUSD float64   `json:"activeComputeHourlyUsd,omitempty"`
	CreatedAt              time.Time `json:"createdAt"`
	TerminatedAt           time.Time `json:"terminatedAt"`
	UptimeHours            float64   `json:"uptimeHours"`
	ActiveHours            float64   `json:"activeHours"`
	HibernatedHours        float64   `json:"hibernatedHours"`
	AccruedCostUSD         float64   `json:"accruedCostUsd"`
	HibernationSavingsUSD  float64   `json:"hibernationSavingsUsd"`
	DeltaRoutingSavingsUSD float64   `json:"deltaRoutingSavingsUsd"`
}

func previewGroupNameFromEnv(env *v1alpha1.Environment) string {
	if env == nil || len(env.Labels) == 0 {
		return ""
	}
	if pg := strings.TrimSpace(env.Labels["divergedev.com/preview-group"]); pg != "" {
		return pg
	}
	return strings.TrimSpace(env.Labels["divergedev.com/previewgroup"])
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
	return SnapshotEnvironmentTeardownWithGroup(env, now, rateCard, baselineServiceCount, 0, 0)
}

// SnapshotEnvironmentTeardownWithGroup builds a HistoricalEnvironmentRecord while allocating PreviewGroup
// delta-routing savings across the group's deployed services rather than counting full baseline savings per child.
func SnapshotEnvironmentTeardownWithGroup(
	env *v1alpha1.Environment,
	now time.Time,
	rateCard metrics.CostRateCard,
	baselineServiceCount int,
	groupDeployedServices int,
	groupPerServiceHourlyUSD float64,
) HistoricalEnvironmentRecord {
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
	activeComputeHourly := est.BaseComputeHourlyUSD
	hibernationSavingsUSD := 0.0
	if est.HibernatedHours > 0 && activeComputeHourly > 0 {
		hibernationSavingsUSD = est.HibernatedHours * activeComputeHourly
	}

	effectiveGroupServices := groupDeployedServices
	if effectiveGroupServices < est.ServiceCount {
		effectiveGroupServices = est.ServiceCount
	}
	effectivePerSvcHourly := groupPerServiceHourlyUSD
	if effectivePerSvcHourly <= 0 && est.ServiceCount > 0 && activeComputeHourly > 0 {
		effectivePerSvcHourly = activeComputeHourly / float64(est.ServiceCount)
	}

	deltaSavingsUSD := 0.0
	if env.Spec.Deploy.Namespace != "create" &&
		est.ServiceCount > 0 &&
		effectiveGroupServices > 0 &&
		baselineServiceCount > effectiveGroupServices &&
		effectivePerSvcHourly > 0 &&
		est.ActiveHours > 0 {
		avoidedGroupServices := float64(baselineServiceCount - effectiveGroupServices)
		memberShare := float64(est.ServiceCount) / float64(effectiveGroupServices)
		deltaSavingsUSD = est.ActiveHours * (avoidedGroupServices * memberShare) * effectivePerSvcHourly
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

	cpuCores := est.CPUCores
	if cpuCores <= 0 {
		cpuCores = est.ConfiguredCPUCores
	}
	memoryGiB := est.MemoryGiB
	if memoryGiB <= 0 {
		memoryGiB = est.ConfiguredMemoryGiB
	}

	return HistoricalEnvironmentRecord{
		UID:                    string(env.UID),
		Name:                   env.Name,
		Namespace:              ns,
		Cluster:                multicluster.ClusterName(env),
		PreviewGroup:           previewGroupNameFromEnv(env),
		Project:                project,
		MR:                     mrStr,
		Branch:                 env.Spec.Source.Branch,
		DatabaseMode:           metrics.ResolveEnvironmentDatabaseMode(env),
		ServiceCount:           int32(est.ServiceCount),
		CPUCores:               cpuCores,
		MemoryGiB:              memoryGiB,
		ActiveComputeHourlyUSD: activeComputeHourly,
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

type pgIntervalMember struct {
	key               string
	start             time.Time
	end               time.Time
	services          int
	activeHours       float64
	baseComputeHourly float64
}

func envDedupKey(ns string, env *v1alpha1.Environment, start time.Time) string {
	if env != nil && strings.TrimSpace(string(env.UID)) != "" {
		return "uid:" + strings.TrimSpace(string(env.UID))
	}
	name := ""
	if env != nil {
		name = env.Name
	}
	return fmt.Sprintf("env:%s/%s@%d", ns, name, start.UTC().Unix())
}

func buildEnvPGIntervalMember(ns string, env *v1alpha1.Environment, refNow time.Time, rateCard metrics.CostRateCard) (pgIntervalMember, metrics.EnvironmentCostEstimate, bool) {
	if env == nil || env.Spec.Deploy.Namespace == "create" {
		return pgIntervalMember{}, metrics.EnvironmentCostEstimate{}, false
	}
	est := metrics.ComputeEnvironmentCost(env, refNow, rateCard)
	if est.ServiceCount <= 0 {
		return pgIntervalMember{}, est, false
	}
	start := env.CreationTimestamp.UTC()
	if start.IsZero() && env.Status.CreatedAt != nil {
		start = env.Status.CreatedAt.UTC()
	}
	if start.IsZero() || start.After(refNow) {
		start = refNow
	}
	end := refNow
	terminal := env.Status.Phase == v1alpha1.PhaseFailed ||
		env.Status.Phase == v1alpha1.PhaseTerminating ||
		string(env.Status.Phase) == "Expired"
	if terminal {
		billableHours := est.ActiveHours + est.HibernatedHours
		if billableHours > 0 {
			end = start.Add(time.Duration(billableHours * float64(time.Hour)))
			if end.After(refNow) {
				end = refNow
			}
		} else {
			end = start
		}
	}
	return pgIntervalMember{
		key:               envDedupKey(ns, env, start),
		start:             start,
		end:               end,
		services:          est.ServiceCount,
		activeHours:       est.ActiveHours,
		baseComputeHourly: est.BaseComputeHourlyUSD,
	}, est, true
}

func historicalRecordBaseComputeHourly(rec HistoricalEnvironmentRecord, rateCard metrics.CostRateCard) float64 {
	if rec.ActiveComputeHourlyUSD > 0 {
		return rec.ActiveComputeHourlyUSD
	}
	if rec.ServiceCount <= 0 {
		return 0
	}
	cpu := rec.CPUCores
	mem := rec.MemoryGiB
	if cpu <= 0 {
		cpu = float64(rec.ServiceCount) * metrics.DefaultServiceCPUCores
	}
	if mem <= 0 {
		mem = float64(rec.ServiceCount) * metrics.DefaultServiceMemoryGiB
	}
	rc := rateCard
	if rc.VCPUHourUSD == 0 && rc.MemoryGiBHourUSD == 0 && rc.DatabaseSchemaHrUSD == 0 && rc.DatabaseDedicatedHrUSD == 0 {
		rc = metrics.DefaultCostRateCard()
	}
	return cpu*rc.VCPUHourUSD + mem*rc.MemoryGiBHourUSD
}

func buildHistoricalPGIntervalMember(rec HistoricalEnvironmentRecord, rateCard metrics.CostRateCard) (pgIntervalMember, bool) {
	if rec.ServiceCount <= 0 {
		return pgIntervalMember{}, false
	}
	start := rec.CreatedAt.UTC()
	end := rec.TerminatedAt.UTC()
	if !end.After(start) {
		return pgIntervalMember{}, false
	}
	return pgIntervalMember{
		key:               rec.dedupKey(),
		start:             start,
		end:               end,
		services:          int(rec.ServiceCount),
		activeHours:       rec.ActiveHours,
		baseComputeHourly: historicalRecordBaseComputeHourly(rec, rateCard),
	}, true
}

// allocatePreviewGroupIntervalSavings splits PreviewGroup delta-routing savings across elementary
// sibling creation and termination intervals so each member receives savings proportional to the
// actual active group membership during every sub-interval of its lifetime.
func allocatePreviewGroupIntervalSavings(baselineCount int, members []pgIntervalMember) map[string]float64 {
	savingsByKey := make(map[string]float64, len(members))
	if baselineCount <= 0 || len(members) == 0 {
		return savingsByKey
	}

	var boundaries []time.Time
	valid := make([]pgIntervalMember, 0, len(members))
	for _, m := range members {
		if m.services <= 0 || !m.end.After(m.start) {
			continue
		}
		valid = append(valid, m)
		boundaries = append(boundaries, m.start, m.end)
	}
	if len(valid) == 0 {
		return savingsByKey
	}

	sort.Slice(boundaries, func(i, j int) bool {
		return boundaries[i].Before(boundaries[j])
	})
	uniq := boundaries[:0]
	for _, b := range boundaries {
		if len(uniq) == 0 || !uniq[len(uniq)-1].Equal(b) {
			uniq = append(uniq, b)
		}
	}

	for idx := 0; idx+1 < len(uniq); idx++ {
		segStart := uniq[idx]
		segEnd := uniq[idx+1]
		segHours := segEnd.Sub(segStart).Hours()
		if segHours <= 0 {
			continue
		}

		var groupServices int
		var groupHourlyUSD float64
		var activeIdxs []int
		for mIdx, m := range valid {
			if !m.start.After(segStart) && !m.end.Before(segEnd) {
				groupServices += m.services
				groupHourlyUSD += m.baseComputeHourly
				activeIdxs = append(activeIdxs, mIdx)
			}
		}
		if groupServices <= 0 || baselineCount <= groupServices || groupHourlyUSD <= 0 {
			continue
		}

		avoidedServices := float64(baselineCount - groupServices)
		perServiceHourly := groupHourlyUSD / float64(groupServices)

		for _, mIdx := range activeIdxs {
			m := valid[mIdx]
			if m.activeHours <= 0 {
				continue
			}
			totalSpanHours := m.end.Sub(m.start).Hours()
			if totalSpanHours <= 0 {
				continue
			}
			activeRatio := m.activeHours / totalSpanHours
			if activeRatio > 1 {
				activeRatio = 1
			}
			segActiveHours := segHours * activeRatio
			memberShare := float64(m.services) / float64(groupServices)
			savingsByKey[m.key] += segActiveHours * (avoidedServices * memberShare) * perServiceHourly
		}
	}
	return savingsByKey
}

func computePreviewGroupTeardownSnapshot(
	ctx context.Context,
	c client.Client,
	env *v1alpha1.Environment,
	ns, pgName string,
	termTime time.Time,
	rateCard metrics.CostRateCard,
	initialBaseline int,
	existingLedger []HistoricalEnvironmentRecord,
) HistoricalEnvironmentRecord {
	record := SnapshotEnvironmentTeardown(env, termTime, rateCard, initialBaseline)
	if pgName == "" || env.Spec.Deploy.Namespace == "create" || record.ServiceCount <= 0 {
		return record
	}

	baselineCount := initialBaseline
	selfMember, _, ok := buildEnvPGIntervalMember(ns, env, termTime, rateCard)
	if !ok {
		return record
	}
	membersByKey := map[string]pgIntervalMember{
		selfMember.key: selfMember,
	}

	qCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// Inspect parent PreviewGroup if present for authoritative total service count.
	var pgList v1alpha1.PreviewGroupList
	if err := c.List(qCtx, &pgList); err == nil {
		for i := range pgList.Items {
			pg := &pgList.Items[i]
			if pg.Name != pgName {
				continue
			}
			if pg.Namespace != "" && pg.Namespace != ns {
				continue
			}
			totalSvcs := len(pg.Spec.Services)
			if int(pg.Status.ServiceCount) > totalSvcs {
				totalSvcs = int(pg.Status.ServiceCount)
			}
			if totalSvcs > baselineCount {
				baselineCount = totalSvcs
			}
			break
		}
	}

	// Include live/terminating sibling Environments belonging to the same PreviewGroup.
	var envList v1alpha1.EnvironmentList
	if err := c.List(qCtx, &envList, client.InNamespace(ns)); err == nil {
		for i := range envList.Items {
			sib := &envList.Items[i]
			if sib.Name == env.Name || sib.Spec.Deploy.Namespace == "create" {
				continue
			}
			if previewGroupNameFromEnv(sib) != pgName {
				continue
			}
			if raw := strings.TrimSpace(sib.Annotations[AnnotationBaselineServiceCount]); raw != "" {
				if parsed, pErr := strconv.Atoi(raw); pErr == nil && parsed > baselineCount {
					baselineCount = parsed
				}
			}
			if sibMember, _, sibOK := buildEnvPGIntervalMember(ns, sib, termTime, rateCard); sibOK {
				membersByKey[sibMember.key] = sibMember
			}
		}
	}

	// Include sibling environments from the same PreviewGroup that already tore down earlier.
	for _, rec := range existingLedger {
		if rec.PreviewGroup != pgName || rec.Namespace != ns {
			continue
		}
		histMember, histOK := buildHistoricalPGIntervalMember(rec, rateCard)
		if !histOK {
			continue
		}
		if _, seen := membersByKey[histMember.key]; seen {
			continue
		}
		membersByKey[histMember.key] = histMember
	}

	members := make([]pgIntervalMember, 0, len(membersByKey))
	for _, m := range membersByKey {
		members = append(members, m)
	}
	allocated := allocatePreviewGroupIntervalSavings(baselineCount, members)
	record.DeltaRoutingSavingsUSD = allocated[selfMember.key]
	return record
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

	pgName := previewGroupNameFromEnv(env)
	termTime := now.UTC()
	if termTime.IsZero() {
		termTime = time.Now().UTC()
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var cm corev1.ConfigMap
		key := types.NamespacedName{Namespace: ns, Name: LedgerConfigMapName}
		err := c.Get(ctx, key, &cm)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("getting finops ledger configmap %s/%s: %w", ns, LedgerConfigMapName, err)
			}
			record := computePreviewGroupTeardownSnapshot(ctx, c, env, ns, pgName, termTime, rateCard, baselineCount, nil)
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
		record := computePreviewGroupTeardownSnapshot(ctx, c, env, ns, pgName, termTime, rateCard, baselineCount, existing)
		cutoff := record.TerminatedAt.AddDate(0, 0, -LedgerRetentionDays)
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
