package finops

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/metrics"
)

const (
	AnnotationBaselineServiceCount  = "divergedev.com/baseline-service-count"
	AnnotationMaxActiveEnvironments = "divergedev.com/max-active-environments"
	AnnotationMaxCNPGClusters       = "divergedev.com/max-cnpg-clusters"
	AnnotationMaxHourlyCostUSD      = "divergedev.com/max-hourly-cost-usd"
	AnnotationQuotaExceedPolicy     = "divergedev.com/quota-exceed-policy"

	QuotaPolicyHibernateOldest = "hibernate-oldest"
	QuotaPolicyReject          = "reject"

	HoursPerMonth                 = 730.0
	BudgetWarningThresholdPercent = 80.0
	defaultSummaryTimeout         = 5 * time.Second
	defaultTTLHours               = 24.0
	longTTLWarningThreshold       = 72 * time.Hour
	expiringSoonWarningThreshold  = 30 * time.Minute
)

// SummaryOptions configures FinOps fleet and namespace cost aggregation.
type SummaryOptions struct {
	Namespace            string
	Client               client.Reader
	K8sClient            kubernetes.Interface
	Now                  time.Time
	RateCard             *metrics.CostRateCard
	OpenCost             *OpenCostClient
	CanListPreviewGroups func(ctx context.Context) bool
	CanListAgentTasks    func(ctx context.Context) bool
	Timeout              time.Duration
}

// BuildCostSummary computes per-namespace, per-service, and per-PR usage and cost attribution
// across preview environments, optionally enriched by OpenCost live allocation data.
func BuildCostSummary(ctx context.Context, opts SummaryOptions) (*pb.GetCostSummaryResponse, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultSummaryTimeout
	}

	reqNS := strings.TrimSpace(opts.Namespace)
	listOpts := make([]client.ListOption, 0, 1)
	if reqNS != "" {
		listOpts = append(listOpts, client.InNamespace(reqNS))
	}

	var envList v1alpha1.EnvironmentList
	if opts.Client != nil {
		listCtx, listCancel := context.WithTimeout(ctx, timeout)
		err := opts.Client.List(listCtx, &envList, listOpts...)
		listCancel()
		if err != nil {
			return nil, err
		}
	}

	pgBaselineByNS := collectPreviewGroupBaselineCounts(ctx, opts.Client, reqNS, timeout, opts.CanListPreviewGroups)
	agentTaskSpendUSD, err := collectAgentTaskSpendUSD(ctx, opts.Client, listOpts, timeout, opts.CanListAgentTasks)
	if err != nil {
		return nil, err
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	rateCard := metrics.CostRateCardFromEnv()
	if opts.RateCard != nil {
		rateCard = *opts.RateCard
	}

	openCost := opts.OpenCost
	if openCost == nil {
		openCost = OpenCostClientFromEnv()
	}
	var (
		liveAllocs      map[string]EnvironmentAllocation
		openCostQueried bool
	)
	if openCost != nil {
		var openCostNamespaces []string
		if reqNS != "" {
			openCostNamespaces = []string{reqNS}
			for i := range envList.Items {
				env := &envList.Items[i]
				if !env.DeletionTimestamp.IsZero() {
					continue
				}
				if wns := resolveWorkloadNamespace(env, reqNS); wns != "" && wns != reqNS {
					openCostNamespaces = append(openCostNamespaces, wns)
				}
			}
		}
		if fetched, fetchErr := openCost.FetchAllocationsForNamespaces(ctx, openCostNamespaces); fetchErr == nil {
			liveAllocs = fetched
			openCostQueried = true
		}
	}

	nsBreakdowns := make(map[string]*pb.NamespaceCostBreakdown)
	svcBreakdowns := make(map[string]*pb.ServiceCostBreakdown)
	baselineServiceCache := make(map[string]int)

	resolveBaselineServices := func(ns string, env *v1alpha1.Environment) int {
		if env != nil {
			if raw := strings.TrimSpace(env.Annotations[AnnotationBaselineServiceCount]); raw != "" {
				if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 {
					return parsed
				}
			}
		}
		if cached, ok := baselineServiceCache[ns]; ok {
			return cached
		}
		count := pgBaselineByNS[ns]
		if depCount := countBaselineDeployments(ctx, opts.Client, opts.K8sClient, ns, timeout); depCount > count {
			count = depCount
		}
		baselineServiceCache[ns] = count
		return count
	}

	type previewGroupDeltaState struct {
		deployedServices int
		computeHourlyUSD float64
		baselineCount    int
	}
	pgDeltaByKey := make(map[string]*previewGroupDeltaState)
	cnpgCountsByNS := make(map[string]int32)
	topConsumers := make([]*pb.EnvironmentCostItem, 0, len(envList.Items))

	var (
		totalHourlyUSD             float64
		totalHibernationSavingsUSD float64
		totalDeltaSavingsUSD       float64
		totalAccruedUSD            float64
		totalComputeHours          float64
		totalCPUCores              float64
		totalMemoryGiB             float64
		totalEnvironments          int32
		activeEnvironments         int32
		hibernatedEnvironments     int32
	)

	for i := range envList.Items {
		env := &envList.Items[i]
		if !env.DeletionTimestamp.IsZero() {
			continue
		}

		ns := strings.TrimSpace(env.Namespace)
		if ns == "" {
			ns = "default"
		}

		est := metrics.ComputeEnvironmentCost(env, now, rateCard)
		terminal := env.Status.Phase == v1alpha1.PhaseFailed ||
			env.Status.Phase == v1alpha1.PhaseTerminating ||
			string(env.Status.Phase) == "Expired"

		if !terminal && !est.Hibernated && openCostQueried {
			workloadNS := resolveWorkloadNamespace(env, ns)
			if live, ok := liveAllocs[workloadNS+"/"+env.Name]; ok {
				est = applyLiveAllocation(env, est, live)
			} else if workloadNS != ns {
				if live, ok := liveAllocs[ns+"/"+env.Name]; ok {
					est = applyLiveAllocation(env, est, live)
				} else {
					log.FromContext(ctx).V(1).Info("No OpenCost allocation entry matched active environment; using rate-card estimate",
						"namespace", ns, "workloadNamespace", workloadNS, "environment", env.Name)
				}
			} else {
				log.FromContext(ctx).V(1).Info("No OpenCost allocation entry matched active environment; using rate-card estimate",
					"namespace", ns, "workloadNamespace", workloadNS, "environment", env.Name)
			}
		}

		totalEnvironments++
		if est.Hibernated {
			hibernatedEnvironments++
		} else if !terminal {
			activeEnvironments++
		}
		activeComputeHours := est.ActiveHours * float64(est.ServiceCount)

		dbMode := metrics.ResolveEnvironmentDatabaseMode(env)
		if !terminal && (dbMode == "cnpg" || dbMode == "fresh" || dbMode == "snapshot") {
			cnpgCountsByNS[ns]++
		}

		totalHourlyUSD += est.HourlyUSD
		totalHibernationSavingsUSD += est.HibernationSavingsHourlyUSD
		totalAccruedUSD += est.AccruedUSD
		totalComputeHours += activeComputeHours
		totalCPUCores += est.CPUCores
		totalMemoryGiB += est.MemoryGiB

		if !terminal && !est.Hibernated && env.Spec.Deploy.Namespace != "create" && est.ServiceCount > 0 {
			baselineCount := resolveBaselineServices(ns, env)
			pgName := strings.TrimSpace(env.Labels["divergedev.com/preview-group"])
			if pgName != "" {
				pgKey := ns + "/" + pgName
				state, exists := pgDeltaByKey[pgKey]
				if !exists {
					state = &previewGroupDeltaState{}
					pgDeltaByKey[pgKey] = state
				}
				state.deployedServices += est.ServiceCount
				state.computeHourlyUSD += est.ComputeHourlyUSD
				if baselineCount > state.baselineCount {
					state.baselineCount = baselineCount
				}
			} else if baselineCount > est.ServiceCount {
				perServiceComputeHourly := est.ComputeHourlyUSD / float64(est.ServiceCount)
				totalDeltaSavingsUSD += float64(baselineCount-est.ServiceCount) * perServiceComputeHourly
			}
		}

		nsEntry, ok := nsBreakdowns[ns]
		if !ok {
			nsEntry = &pb.NamespaceCostBreakdown{Namespace: ns}
			nsBreakdowns[ns] = nsEntry
		}
		nsEntry.TotalEnvironments++
		if est.Hibernated {
			nsEntry.HibernatedEnvironments++
		} else if !terminal {
			nsEntry.ActiveEnvironments++
		}
		nsEntry.HourlyCostUsd += est.HourlyUSD
		nsEntry.HibernationSavingsHourlyUsd += est.HibernationSavingsHourlyUSD
		nsEntry.AccruedCostUsd += est.AccruedUSD
		nsEntry.ComputeHours += activeComputeHours
		nsEntry.CpuCores += est.CPUCores
		nsEntry.MemoryGib += est.MemoryGiB

		services := ExtractEnvironmentServiceNames(env)
		share := float64(len(services))
		for _, svcName := range services {
			svcEntry, exists := svcBreakdowns[svcName]
			if !exists {
				svcEntry = &pb.ServiceCostBreakdown{ServiceName: svcName}
				svcBreakdowns[svcName] = svcEntry
			}
			svcEntry.EnvironmentCount++
			if est.Hibernated {
				svcEntry.HibernatedCount++
			} else if !terminal {
				svcEntry.ActiveCount++
			}
			svcEntry.HourlyCostUsd += est.HourlyUSD / share
			svcEntry.CpuCores += est.CPUCores / share
			svcEntry.MemoryGib += est.MemoryGiB / share
		}

		ttlWarn, ttlReason := EvaluateEnvironmentTTLWarning(env, est.Hibernated, terminal, now)
		phase := string(env.Status.Phase)
		if phase == "" {
			phase = string(v1alpha1.PhasePending)
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

		topConsumers = append(topConsumers, &pb.EnvironmentCostItem{
			Name:                        env.Name,
			Namespace:                   ns,
			Project:                     project,
			Mr:                          mrStr,
			Branch:                      env.Spec.Source.Branch,
			Phase:                       phase,
			Hibernated:                  est.Hibernated,
			HourlyCostUsd:               est.HourlyUSD,
			HibernationSavingsHourlyUsd: est.HibernationSavingsHourlyUSD,
			AccruedCostUsd:              est.AccruedUSD,
			ProjectedTtlCostUsd:         est.ProjectedTTLUSD,
			UptimeHours:                 est.UptimeHours,
			CpuCores:                    est.CPUCores,
			MemoryGib:                   est.MemoryGiB,
			ServiceCount:                int32(est.ServiceCount),
			DatabaseMode:                dbMode,
			TtlWarning:                  ttlWarn,
			TtlWarningReason:            ttlReason,
		})
	}

	for _, state := range pgDeltaByKey {
		if state.deployedServices > 0 && state.baselineCount > state.deployedServices {
			perServiceComputeHourly := state.computeHourlyUSD / float64(state.deployedServices)
			totalDeltaSavingsUSD += float64(state.baselineCount-state.deployedServices) * perServiceComputeHourly
		}
	}

	if reqNS != "" {
		if _, exists := nsBreakdowns[reqNS]; !exists {
			if q := GetNamespaceQuota(ctx, opts.Client, opts.K8sClient, reqNS, timeout); q != nil {
				nsBreakdowns[reqNS] = &pb.NamespaceCostBreakdown{
					Namespace: reqNS,
					Quota:     q,
				}
			}
		}
	}

	byNamespace := make([]*pb.NamespaceCostBreakdown, 0, len(nsBreakdowns))
	for ns, entry := range nsBreakdowns {
		if entry.Quota == nil {
			entry.Quota = GetNamespaceQuota(ctx, opts.Client, opts.K8sClient, ns, timeout)
		}
		thresholdPct := resolveNamespaceAlertThreshold(ctx, opts.Client, opts.K8sClient, ns, timeout)
		PopulateNamespaceBudgetWarning(entry, cnpgCountsByNS[ns], thresholdPct)
		byNamespace = append(byNamespace, entry)
	}
	sort.Slice(byNamespace, func(i, j int) bool {
		if math.Abs(byNamespace[i].HourlyCostUsd-byNamespace[j].HourlyCostUsd) > 1e-9 {
			return byNamespace[i].HourlyCostUsd > byNamespace[j].HourlyCostUsd
		}
		return byNamespace[i].Namespace < byNamespace[j].Namespace
	})

	byService := make([]*pb.ServiceCostBreakdown, 0, len(svcBreakdowns))
	for _, entry := range svcBreakdowns {
		byService = append(byService, entry)
	}
	sort.Slice(byService, func(i, j int) bool {
		if math.Abs(byService[i].HourlyCostUsd-byService[j].HourlyCostUsd) > 1e-9 {
			return byService[i].HourlyCostUsd > byService[j].HourlyCostUsd
		}
		if byService[i].EnvironmentCount != byService[j].EnvironmentCount {
			return byService[i].EnvironmentCount > byService[j].EnvironmentCount
		}
		return byService[i].ServiceName < byService[j].ServiceName
	})

	sort.Slice(topConsumers, func(i, j int) bool {
		if math.Abs(topConsumers[i].HourlyCostUsd-topConsumers[j].HourlyCostUsd) > 1e-9 {
			return topConsumers[i].HourlyCostUsd > topConsumers[j].HourlyCostUsd
		}
		if math.Abs(topConsumers[i].AccruedCostUsd-topConsumers[j].AccruedCostUsd) > 1e-9 {
			return topConsumers[i].AccruedCostUsd > topConsumers[j].AccruedCostUsd
		}
		if topConsumers[i].Namespace != topConsumers[j].Namespace {
			return topConsumers[i].Namespace < topConsumers[j].Namespace
		}
		return topConsumers[i].Name < topConsumers[j].Name
	})

	return &pb.GetCostSummaryResponse{
		TotalHourlyCostUsd:           totalHourlyUSD,
		MonthlyRunRateUsd:            totalHourlyUSD * HoursPerMonth,
		HibernationSavingsHourlyUsd:  totalHibernationSavingsUSD,
		DeltaRoutingSavingsHourlyUsd: totalDeltaSavingsUSD,
		TotalAccruedCostUsd:          totalAccruedUSD,
		AgentTaskCostUsd:             agentTaskSpendUSD,
		TotalComputeHours:            totalComputeHours,
		TotalCpuCores:                totalCPUCores,
		TotalMemoryGib:               totalMemoryGiB,
		TotalEnvironments:            totalEnvironments,
		ActiveEnvironments:           activeEnvironments,
		HibernatedEnvironments:       hibernatedEnvironments,
		ByNamespace:                  byNamespace,
		ByService:                    byService,
		TopConsumers:                 topConsumers,
		RateCard: &pb.CostRateCardInfo{
			VcpuHourUsd:              rateCard.VCPUHourUSD,
			MemoryGibHourUsd:         rateCard.MemoryGiBHourUSD,
			DatabaseSchemaHourUsd:    rateCard.DatabaseSchemaHrUSD,
			DatabaseDedicatedHourUsd: rateCard.DatabaseDedicatedHrUSD,
		},
	}, nil
}

func resolveWorkloadNamespace(env *v1alpha1.Environment, defaultNS string) string {
	if env != nil && env.Spec.Deploy.Namespace == "create" {
		if pns := strings.TrimSpace(env.PreviewNamespace()); pns != "" {
			return pns
		}
	}
	return defaultNS
}

func applyLiveAllocation(env *v1alpha1.Environment, est metrics.EnvironmentCostEstimate, live EnvironmentAllocation) metrics.EnvironmentCostEstimate {
	if !isFiniteNonNegative(live.HourlyComputeUSD) {
		return est
	}
	est.ComputeHourlyUSD = live.HourlyComputeUSD
	est.HourlyUSD = est.ComputeHourlyUSD + est.DatabaseHourlyUSD
	// Preserve est.AccruedUSD from ComputeEnvironmentCost rather than multiplying the instantaneous
	// 1h live rate across all historical est.ActiveHours, ensuring hibernation never reduces accrued spend.

	ttlHours := defaultTTLHours
	if env != nil && env.Spec.Lifecycle.TTL != nil && env.Spec.Lifecycle.TTL.Duration > 0 {
		ttlHours = env.Spec.Lifecycle.TTL.Hours()
	}
	remainingHours := ttlHours - est.UptimeHours
	if remainingHours < 0 {
		remainingHours = 0
	}
	projected := est.AccruedUSD + est.HourlyUSD*remainingHours
	if projected < est.AccruedUSD {
		projected = est.AccruedUSD
	}
	est.ProjectedTTLUSD = projected

	if isFinitePositive(live.CPUCores) {
		est.CPUCores = live.CPUCores
	}
	if isFinitePositive(live.MemoryGiB) {
		est.MemoryGiB = live.MemoryGiB
	}
	return est
}

// GetNamespaceQuota reads Diverge FinOps quota annotations from a Kubernetes Namespace.
func GetNamespaceQuota(ctx context.Context, reader client.Reader, k8sClient kubernetes.Interface, namespace string, timeout time.Duration) *pb.NamespaceQuotaInfo {
	annotations := getNamespaceAnnotations(ctx, reader, k8sClient, namespace, timeout)
	if len(annotations) == 0 {
		return nil
	}

	var maxActive int32
	if raw := strings.TrimSpace(annotations[AnnotationMaxActiveEnvironments]); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			maxActive = int32(v)
		}
	}

	var maxCNPG int32
	if raw := strings.TrimSpace(annotations[AnnotationMaxCNPGClusters]); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			maxCNPG = int32(v)
		}
	}

	var maxHourlyCost float64
	if raw := strings.TrimPrefix(strings.TrimSpace(annotations[AnnotationMaxHourlyCostUSD]), "$"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 {
			maxHourlyCost = v
		}
	}

	rawPolicy := strings.ToLower(strings.TrimSpace(annotations[AnnotationQuotaExceedPolicy]))
	if maxActive <= 0 && maxCNPG <= 0 && maxHourlyCost <= 0 && rawPolicy == "" {
		return nil
	}

	policy := QuotaPolicyHibernateOldest
	if rawPolicy == QuotaPolicyReject {
		policy = QuotaPolicyReject
	} else if rawPolicy != "" {
		policy = rawPolicy
	}

	return &pb.NamespaceQuotaInfo{
		MaxActiveEnvironments: maxActive,
		MaxCnpgClusters:       maxCNPG,
		MaxHourlyCostUsd:      maxHourlyCost,
		QuotaExceedPolicy:     policy,
	}
}

func resolveNamespaceAlertThreshold(ctx context.Context, reader client.Reader, k8sClient kubernetes.Interface, namespace string, timeout time.Duration) float64 {
	annotations := getNamespaceAnnotations(ctx, reader, k8sClient, namespace, timeout)
	if len(annotations) > 0 {
		if raw := strings.TrimSuffix(strings.TrimSpace(annotations[metrics.AnnotationBudgetAlertThreshold]), "%"); raw != "" {
			if v, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v <= 100 {
				return v
			}
		}
	}
	if raw := strings.TrimSuffix(strings.TrimSpace(os.Getenv(metrics.EnvBudgetAlertThreshold)), "%"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v <= 100 {
			return v
		}
	}
	return BudgetWarningThresholdPercent
}

func getNamespaceAnnotations(ctx context.Context, reader client.Reader, k8sClient kubernetes.Interface, namespace string, timeout time.Duration) map[string]string {
	if strings.TrimSpace(namespace) == "" {
		namespace = "default"
	}
	if k8sClient != nil {
		nsCtx, cancel := context.WithTimeout(ctx, timeout)
		ns, err := k8sClient.CoreV1().Namespaces().Get(nsCtx, namespace, metav1.GetOptions{})
		cancel()
		if err == nil && ns != nil && len(ns.Annotations) > 0 {
			return ns.Annotations
		}
	}
	if reader != nil {
		nsCtx, cancel := context.WithTimeout(ctx, timeout)
		var ns corev1.Namespace
		err := reader.Get(nsCtx, types.NamespacedName{Name: namespace}, &ns)
		cancel()
		if err == nil && len(ns.Annotations) > 0 {
			return ns.Annotations
		}
	}
	return nil
}

func collectPreviewGroupBaselineCounts(ctx context.Context, reader client.Reader, reqNS string, timeout time.Duration, canList func(context.Context) bool) map[string]int {
	counts := make(map[string]int)
	if reader == nil {
		return counts
	}
	if canList != nil {
		authCtx, cancel := context.WithTimeout(ctx, timeout)
		allowed := canList(authCtx)
		cancel()
		if !allowed {
			return counts
		}
	}

	pgCtx, pgCancel := context.WithTimeout(ctx, timeout)
	defer pgCancel()
	var pgList v1alpha1.PreviewGroupList
	opts := make([]client.ListOption, 0, 1)
	if reqNS != "" {
		opts = append(opts, client.InNamespace(reqNS))
	}
	if err := reader.List(pgCtx, &pgList, opts...); err != nil {
		return counts
	}
	for i := range pgList.Items {
		pg := &pgList.Items[i]
		svcCount := len(pg.Spec.Services)
		if int(pg.Status.ServiceCount) > svcCount {
			svcCount = int(pg.Status.ServiceCount)
		}
		ns := strings.TrimSpace(pg.Namespace)
		if ns == "" {
			ns = "default"
		}
		if svcCount > counts[ns] {
			counts[ns] = svcCount
		}
		for _, svc := range pg.Spec.Services {
			svcNS := strings.TrimSpace(svc.Namespace)
			if svcNS == "" {
				svcNS = ns
			}
			if svcCount > counts[svcNS] {
				counts[svcNS] = svcCount
			}
		}
	}
	return counts
}

func countBaselineDeployments(ctx context.Context, reader client.Reader, k8sClient kubernetes.Interface, namespace string, timeout time.Duration) int {
	if namespace == "" {
		return 0
	}
	if k8sClient != nil {
		nsCtx, nsCancel := context.WithTimeout(ctx, timeout)
		nsObj, nsErr := k8sClient.CoreV1().Namespaces().Get(nsCtx, namespace, metav1.GetOptions{})
		nsCancel()
		if nsErr == nil && nsObj != nil {
			if raw := strings.TrimSpace(nsObj.Annotations[AnnotationBaselineServiceCount]); raw != "" {
				if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
					return parsed
				}
			}
		}

		depCtx, depCancel := context.WithTimeout(ctx, timeout)
		deps, depErr := k8sClient.AppsV1().Deployments(namespace).List(depCtx, metav1.ListOptions{})
		depCancel()
		if depErr == nil && deps != nil && len(deps.Items) > 0 {
			baseline := 0
			for i := range deps.Items {
				if strings.TrimSpace(deps.Items[i].Labels["divergedev.com/environment"]) == "" {
					baseline++
				}
			}
			if baseline > 0 {
				return baseline
			}
		}
	}

	if reader != nil {
		nsCtx, nsCancel := context.WithTimeout(ctx, timeout)
		var nsObj corev1.Namespace
		nsErr := reader.Get(nsCtx, types.NamespacedName{Name: namespace}, &nsObj)
		nsCancel()
		if nsErr == nil {
			if raw := strings.TrimSpace(nsObj.Annotations[AnnotationBaselineServiceCount]); raw != "" {
				if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
					return parsed
				}
			}
		}

		depCtx, depCancel := context.WithTimeout(ctx, timeout)
		var depList appsv1.DeploymentList
		depErr := reader.List(depCtx, &depList, client.InNamespace(namespace))
		depCancel()
		if depErr == nil && len(depList.Items) > 0 {
			baseline := 0
			for i := range depList.Items {
				if strings.TrimSpace(depList.Items[i].Labels["divergedev.com/environment"]) == "" {
					baseline++
				}
			}
			return baseline
		}
	}
	return 0
}

func collectAgentTaskSpendUSD(ctx context.Context, reader client.Reader, listOpts []client.ListOption, timeout time.Duration, canList func(context.Context) bool) (float64, error) {
	if reader == nil {
		return 0, nil
	}
	if canList != nil {
		authCtx, cancel := context.WithTimeout(ctx, timeout)
		allowed := canList(authCtx)
		cancel()
		if !allowed {
			return 0, nil
		}
	}

	taskCtx, taskCancel := context.WithTimeout(ctx, timeout)
	defer taskCancel()
	var taskList v1alpha1.AgentTaskList
	if err := reader.List(taskCtx, &taskList, listOpts...); err != nil {
		if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) || apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
			return 0, nil
		}
		return 0, err
	}

	var total float64
	for i := range taskList.Items {
		raw := strings.TrimPrefix(strings.TrimSpace(taskList.Items[i].Status.CostUSD), "$")
		if raw == "" {
			continue
		}
		if parsed, parseErr := strconv.ParseFloat(raw, 64); parseErr == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) && parsed > 0 {
			total += parsed
		}
	}
	return total, nil
}

// ExtractEnvironmentServiceNames extracts sorted unique service names attributed to env.
func ExtractEnvironmentServiceNames(env *v1alpha1.Environment) []string {
	if env == nil {
		return []string{"unknown"}
	}
	seen := make(map[string]struct{})
	var names []string
	add := func(raw string) {
		s := strings.TrimSpace(raw)
		if s == "" {
			return
		}
		if _, exists := seen[s]; !exists {
			seen[s] = struct{}{}
			names = append(names, s)
		}
	}
	for _, s := range env.Spec.Deploy.ChangedServices {
		add(s)
	}
	for _, s := range env.Status.Services {
		add(s)
	}
	if len(names) == 0 && env.Spec.ServiceConfig != nil {
		add(env.Spec.ServiceConfig.ServiceName)
	}
	if len(names) == 0 {
		add(env.Name)
	}
	sort.Strings(names)
	return names
}

// EvaluateEnvironmentTTLWarning evaluates TTL configuration and remaining lifetime for warning badges.
func EvaluateEnvironmentTTLWarning(env *v1alpha1.Environment, hibernated, terminal bool, now time.Time) (bool, string) {
	if env == nil || terminal {
		return false, ""
	}
	if env.Status.ExpiresAt != nil && !env.Status.ExpiresAt.IsZero() {
		remaining := env.Status.ExpiresAt.Sub(now)
		if remaining <= 0 {
			return true, "TTL expired; pending teardown"
		}
		if remaining <= expiringSoonWarningThreshold {
			return true, "Expiring within 30m"
		}
	} else if env.Spec.Lifecycle.TTL == nil || env.Spec.Lifecycle.TTL.Duration <= 0 {
		return true, "No TTL configured (unbounded lifetime)"
	}

	if !hibernated && env.Spec.Lifecycle.TTL != nil && env.Spec.Lifecycle.TTL.Duration > longTTLWarningThreshold {
		threshold, enabled := metrics.EffectiveIdleHibernateThreshold(env, 0)
		if !enabled || threshold > longTTLWarningThreshold || threshold >= env.Spec.Lifecycle.TTL.Duration {
			return true, "Long TTL (>72h) without effective idle hibernation"
		}
	}
	return false, ""
}

// PopulateNamespaceBudgetWarning evaluates namespace spend, active preview, and CNPG cluster quotas.
func PopulateNamespaceBudgetWarning(entry *pb.NamespaceCostBreakdown, cnpgCount int32, warningThresholdPercent float64) {
	if entry == nil || entry.Quota == nil {
		return
	}
	if warningThresholdPercent <= 0 || warningThresholdPercent > 100 {
		warningThresholdPercent = BudgetWarningThresholdPercent
	}

	var reasons []string
	var maxUtil float64

	if entry.Quota.MaxHourlyCostUsd > 0 {
		costPct := (entry.HourlyCostUsd / entry.Quota.MaxHourlyCostUsd) * 100.0
		if costPct > maxUtil {
			maxUtil = costPct
		}
		if costPct >= warningThresholdPercent {
			reasons = append(reasons, fmt.Sprintf("Hourly spend at %.0f%% ($%.3f / $%.3f/hr)", costPct, entry.HourlyCostUsd, entry.Quota.MaxHourlyCostUsd))
		}
	}

	if entry.Quota.MaxActiveEnvironments > 0 {
		activePct := (float64(entry.ActiveEnvironments) / float64(entry.Quota.MaxActiveEnvironments)) * 100.0
		if activePct > maxUtil {
			maxUtil = activePct
		}
		if activePct >= warningThresholdPercent {
			reasons = append(reasons, fmt.Sprintf("Active environments at %d/%d (%.0f%% of quota)", entry.ActiveEnvironments, entry.Quota.MaxActiveEnvironments, activePct))
		}
	}

	if entry.Quota.MaxCnpgClusters > 0 {
		cnpgPct := (float64(cnpgCount) / float64(entry.Quota.MaxCnpgClusters)) * 100.0
		if cnpgPct > maxUtil {
			maxUtil = cnpgPct
		}
		if cnpgPct >= warningThresholdPercent {
			reasons = append(reasons, fmt.Sprintf("CNPG clusters at %d/%d (%.0f%% of quota)", cnpgCount, entry.Quota.MaxCnpgClusters, cnpgPct))
		}
	}

	entry.BudgetUtilizationPercent = maxUtil
	if len(reasons) > 0 {
		entry.BudgetWarning = true
		entry.WarningReason = strings.Join(reasons, "; ")
	}
}
