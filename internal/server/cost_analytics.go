package server

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/metrics"
)

const (
	annotationBaselineServiceCount = "divergedev.com/baseline-service-count"
	hoursPerMonth                  = 730.0
	budgetWarningThresholdPercent  = 80.0
	longTTLWarningThreshold        = 72 * time.Hour
	expiringSoonWarningThreshold   = 30 * time.Minute
)

// GetCostSummary computes per-namespace, per-service, and per-PR usage and cost attribution
// across preview environments, including hibernation savings, delta-routing savings, AgentTask LLM spend,
// and quota/TTL budget warnings.
func (s *ClusterService) GetCostSummary(ctx context.Context, req *connect.Request[pb.GetCostSummaryRequest]) (*connect.Response[pb.GetCostSummaryResponse], error) {
	const apiTimeout = 5 * time.Second

	reqNS := ""
	if req != nil && req.Msg != nil {
		reqNS = strings.TrimSpace(req.Msg.GetNamespace())
	}
	if reqNS != "" {
		if err := ValidateDNS1123Label(reqNS, "namespace"); err != nil {
			return nil, err
		}
	}

	sarCtx, sarCancel := context.WithTimeout(ctx, apiTimeout)
	err := AuthorizeCostSummary(sarCtx, s.k8sClient, s.auditLogger, reqNS)
	sarCancel()
	if err != nil {
		return nil, err
	}

	listOpts := make([]client.ListOption, 0, 1)
	if reqNS != "" {
		listOpts = append(listOpts, client.InNamespace(reqNS))
	}

	listCtx, listCancel := context.WithTimeout(ctx, apiTimeout)
	defer listCancel()
	var envList v1alpha1.EnvironmentList
	if err := s.client.List(listCtx, &envList, listOpts...); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	pgBaselineByNS := s.collectPreviewGroupBaselineCounts(ctx, reqNS, apiTimeout)
	agentTaskSpendUSD, err := s.collectAgentTaskSpendUSD(ctx, reqNS, listOpts, apiTimeout)
	if err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	now := time.Now().UTC()
	rateCard := metrics.CostRateCardFromEnv()

	nsBreakdowns := make(map[string]*pb.NamespaceCostBreakdown)
	svcBreakdowns := make(map[string]*pb.ServiceCostBreakdown)
	baselineServiceCache := make(map[string]int)

	resolveBaselineServices := func(ns string, env *v1alpha1.Environment) int {
		if env != nil {
			if raw := strings.TrimSpace(env.Annotations[annotationBaselineServiceCount]); raw != "" {
				if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 {
					return parsed
				}
			}
		}
		if cached, ok := baselineServiceCache[ns]; ok {
			return cached
		}
		count := pgBaselineByNS[ns]
		if depCount := s.countBaselineDeployments(ctx, ns, apiTimeout); depCount > count {
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

		// Delta routing savings: shared baseline microservices that did NOT need to be duplicated.
		// Group PreviewGroup child Environments so baseline savings are counted once per PreviewGroup.
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

		services := extractEnvironmentServiceNames(env)
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

		ttlWarn, ttlReason := evaluateEnvironmentTTLWarning(env, est.Hibernated, terminal, now)
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

	// Ensure an explicitly requested namespace with configured quotas appears even when empty.
	if reqNS != "" {
		if _, exists := nsBreakdowns[reqNS]; !exists {
			if q := s.getNamespaceQuota(ctx, reqNS, apiTimeout); q != nil {
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
			entry.Quota = s.getNamespaceQuota(ctx, ns, apiTimeout)
		}
		populateNamespaceBudgetWarning(entry, cnpgCountsByNS[ns])
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

	resp := &pb.GetCostSummaryResponse{
		TotalHourlyCostUsd:           totalHourlyUSD,
		MonthlyRunRateUsd:            totalHourlyUSD * hoursPerMonth,
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
	}

	return connect.NewResponse(resp), nil
}

func (s *ClusterService) collectPreviewGroupBaselineCounts(ctx context.Context, reqNS string, timeout time.Duration) map[string]int {
	counts := make(map[string]int)
	if s == nil || s.client == nil {
		return counts
	}
	sarCtx, sarCancel := context.WithTimeout(ctx, timeout)
	err := AuthorizeAction(sarCtx, s.k8sClient, nil, "list", reqNS, "previewgroups")
	sarCancel()
	if err != nil {
		return counts
	}

	pgCtx, pgCancel := context.WithTimeout(ctx, timeout)
	defer pgCancel()
	var pgList v1alpha1.PreviewGroupList
	opts := make([]client.ListOption, 0, 1)
	if reqNS != "" {
		opts = append(opts, client.InNamespace(reqNS))
	}
	if err := s.client.List(pgCtx, &pgList, opts...); err != nil {
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

func (s *ClusterService) countBaselineDeployments(ctx context.Context, namespace string, timeout time.Duration) int {
	if s == nil || namespace == "" {
		return 0
	}
	if s.k8sClient != nil {
		nsCtx, nsCancel := context.WithTimeout(ctx, timeout)
		nsObj, nsErr := s.k8sClient.CoreV1().Namespaces().Get(nsCtx, namespace, metav1.GetOptions{})
		nsCancel()
		if nsErr == nil && nsObj != nil {
			if raw := strings.TrimSpace(nsObj.Annotations[annotationBaselineServiceCount]); raw != "" {
				if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
					return parsed
				}
			}
		}

		depCtx, depCancel := context.WithTimeout(ctx, timeout)
		deps, depErr := s.k8sClient.AppsV1().Deployments(namespace).List(depCtx, metav1.ListOptions{})
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

	if s.client != nil {
		nsCtx, nsCancel := context.WithTimeout(ctx, timeout)
		var nsObj corev1.Namespace
		nsErr := s.client.Get(nsCtx, types.NamespacedName{Name: namespace}, &nsObj)
		nsCancel()
		if nsErr == nil {
			if raw := strings.TrimSpace(nsObj.Annotations[annotationBaselineServiceCount]); raw != "" {
				if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
					return parsed
				}
			}
		}

		depCtx, depCancel := context.WithTimeout(ctx, timeout)
		var depList appsv1.DeploymentList
		depErr := s.client.List(depCtx, &depList, client.InNamespace(namespace))
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

func (s *ClusterService) collectAgentTaskSpendUSD(ctx context.Context, reqNS string, listOpts []client.ListOption, timeout time.Duration) (float64, error) {
	if s == nil || s.client == nil {
		return 0, nil
	}
	sarCtx, sarCancel := context.WithTimeout(ctx, timeout)
	err := AuthorizeAction(sarCtx, s.k8sClient, nil, "list", reqNS, "agenttasks")
	sarCancel()
	if err != nil {
		return 0, nil
	}

	taskCtx, taskCancel := context.WithTimeout(ctx, timeout)
	defer taskCancel()
	var taskList v1alpha1.AgentTaskList
	if err := s.client.List(taskCtx, &taskList, listOpts...); err != nil {
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

func extractEnvironmentServiceNames(env *v1alpha1.Environment) []string {
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

func evaluateEnvironmentTTLWarning(env *v1alpha1.Environment, hibernated, terminal bool, now time.Time) (bool, string) {
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

func populateNamespaceBudgetWarning(entry *pb.NamespaceCostBreakdown, cnpgCount int32) {
	if entry == nil || entry.Quota == nil {
		return
	}
	var reasons []string
	var maxUtil float64

	if entry.Quota.MaxHourlyCostUsd > 0 {
		costPct := (entry.HourlyCostUsd / entry.Quota.MaxHourlyCostUsd) * 100.0
		if costPct > maxUtil {
			maxUtil = costPct
		}
		if costPct >= budgetWarningThresholdPercent {
			reasons = append(reasons, fmt.Sprintf("Hourly spend at %.0f%% ($%.3f / $%.3f/hr)", costPct, entry.HourlyCostUsd, entry.Quota.MaxHourlyCostUsd))
		}
	}

	if entry.Quota.MaxActiveEnvironments > 0 {
		activePct := (float64(entry.ActiveEnvironments) / float64(entry.Quota.MaxActiveEnvironments)) * 100.0
		if activePct > maxUtil {
			maxUtil = activePct
		}
		if activePct >= budgetWarningThresholdPercent {
			reasons = append(reasons, fmt.Sprintf("Active environments at %d/%d (%.0f%% of quota)", entry.ActiveEnvironments, entry.Quota.MaxActiveEnvironments, activePct))
		}
	}

	if entry.Quota.MaxCnpgClusters > 0 {
		cnpgPct := (float64(cnpgCount) / float64(entry.Quota.MaxCnpgClusters)) * 100.0
		if cnpgPct > maxUtil {
			maxUtil = cnpgPct
		}
		if cnpgPct >= budgetWarningThresholdPercent {
			reasons = append(reasons, fmt.Sprintf("CNPG clusters at %d/%d (%.0f%% of quota)", cnpgCount, entry.Quota.MaxCnpgClusters, cnpgPct))
		}
	}

	entry.BudgetUtilizationPercent = maxUtil
	if len(reasons) > 0 {
		entry.BudgetWarning = true
		entry.WarningReason = strings.Join(reasons, "; ")
	}
}
