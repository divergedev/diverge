package controller

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/metrics"
)

const (
	AnnotationMaxActiveEnvironments = "divergedev.com/max-active-environments"
	AnnotationMaxCNPGClusters       = "divergedev.com/max-cnpg-clusters"
	AnnotationMaxHourlyCostUSD      = "divergedev.com/max-hourly-cost-usd"
	AnnotationQuotaExceedPolicy     = "divergedev.com/quota-exceed-policy"
	QuotaPolicyHibernateOldest      = "hibernate-oldest"
	QuotaPolicyReject               = "reject"
	ConditionQuotaReady             = "QuotaReady"
)

func usesCNPGCluster(env *divergeiov1alpha1.Environment) bool {
	if env == nil {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(env.Spec.Database.Mode))
	if mode == "cnpg" || mode == "fresh" || mode == "snapshot" {
		return true
	}
	prov := strings.ToLower(strings.TrimSpace(env.Annotations["divergedev.com/database-provider"]))
	if prov == "cnpg" || prov == "fresh" || prov == "snapshot" {
		return true
	}
	statusProv := strings.ToLower(strings.TrimSpace(env.Status.DatabaseProvider))
	return statusProv == "cnpg" || statusProv == "fresh" || statusProv == "snapshot"
}

func environmentLRUTimestamp(env *divergeiov1alpha1.Environment) time.Time {
	if env == nil {
		return time.Time{}
	}
	if raw := strings.TrimSpace(env.Annotations[metrics.AnnotationLastTrafficAt]); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			return parsed
		}
	}
	if !env.CreationTimestamp.IsZero() {
		return env.CreationTimestamp.Time
	}
	if env.Status.CreatedAt != nil && !env.Status.CreatedAt.IsZero() {
		return env.Status.CreatedAt.Time
	}
	return time.Time{}
}

func (r *EnvironmentReconciler) reconcileNamespaceQuota(ctx context.Context, env *divergeiov1alpha1.Environment, statusBase *divergeiov1alpha1.Environment) (ctrl.Result, bool, error) {
	if env == nil || r.Client == nil {
		return ctrl.Result{}, false, nil
	}

	ns := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: env.Namespace}, ns); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return ctrl.Result{}, false, nil
		}
		return ctrl.Result{}, false, fmt.Errorf("failed to get namespace %s for quota check: %w", env.Namespace, err)
	}
	if len(ns.Annotations) == 0 {
		return ctrl.Result{}, false, nil
	}

	var maxActive int
	if raw := strings.TrimSpace(ns.Annotations[AnnotationMaxActiveEnvironments]); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			maxActive = v
		}
	}

	var maxCNPG int
	if raw := strings.TrimSpace(ns.Annotations[AnnotationMaxCNPGClusters]); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			maxCNPG = v
		}
	}

	var maxHourlyCost float64
	if raw := strings.TrimPrefix(strings.TrimSpace(ns.Annotations[AnnotationMaxHourlyCostUSD]), "$"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v > 0 {
			maxHourlyCost = v
		}
	}

	if maxActive <= 0 && maxCNPG <= 0 && maxHourlyCost <= 0 {
		return ctrl.Result{}, false, nil
	}

	if env.Annotations[metrics.AnnotationHibernated] == "true" {
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    ConditionQuotaReady,
			Status:  metav1.ConditionTrue,
			Reason:  "Hibernated",
			Message: "Environment is hibernated",
		})
		return ctrl.Result{}, false, nil
	}

	policy := strings.ToLower(strings.TrimSpace(ns.Annotations[AnnotationQuotaExceedPolicy]))
	if policy != QuotaPolicyReject {
		policy = QuotaPolicyHibernateOldest
	}

	var envList divergeiov1alpha1.EnvironmentList
	if err := r.List(ctx, &envList, client.InNamespace(env.Namespace)); err != nil {
		return ctrl.Result{}, false, fmt.Errorf("failed to list environments in namespace %s: %w", env.Namespace, err)
	}

	if maxCNPG > 0 && usesCNPGCluster(env) {
		otherCNPGCount := 0
		for i := range envList.Items {
			item := &envList.Items[i]
			if item.Name == env.Name || !item.DeletionTimestamp.IsZero() {
				continue
			}
			if item.Status.Phase == divergeiov1alpha1.PhaseFailed ||
				item.Status.Phase == divergeiov1alpha1.EnvironmentPhase("Expired") ||
				item.Status.Phase == divergeiov1alpha1.PhaseTerminating {
				continue
			}
			if usesCNPGCluster(item) {
				otherCNPGCount++
			}
		}
		if otherCNPGCount+1 > maxCNPG {
			msg := fmt.Sprintf("namespace %s quota exceeded: max-cnpg-clusters=%d", env.Namespace, maxCNPG)
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    ConditionQuotaReady,
				Status:  metav1.ConditionFalse,
				Reason:  "MaxCNPGClustersExceeded",
				Message: msg,
			})
			env.Status.Phase = derivePhase(env.Status.Conditions)
			if r.Recorder != nil && r.Recorder.EventRecorder != nil {
				r.Recorder.Event(env, "Warning", "QuotaExceeded", msg)
			}
			res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, nil, 0)
			return res, true, retErr
		}
	}

	var otherActive []*divergeiov1alpha1.Environment
	for i := range envList.Items {
		item := &envList.Items[i]
		if item.Name == env.Name || !item.DeletionTimestamp.IsZero() {
			continue
		}
		if item.Status.Phase == divergeiov1alpha1.PhaseFailed ||
			item.Status.Phase == divergeiov1alpha1.EnvironmentPhase("Expired") ||
			item.Status.Phase == divergeiov1alpha1.PhaseTerminating {
			continue
		}
		if item.Annotations[metrics.AnnotationHibernated] == "true" {
			continue
		}
		otherActive = append(otherActive, item)
	}

	sort.Slice(otherActive, func(i, j int) bool {
		ti := environmentLRUTimestamp(otherActive[i])
		tj := environmentLRUTimestamp(otherActive[j])
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return otherActive[i].Name < otherActive[j].Name
	})

	now := time.Now()
	rateCard := metrics.DefaultCostRateCard()
	selfCost := metrics.EstimateEnvironmentCost(env, now, rateCard).HourlyUSD

	sumHourlyCost := func(others []*divergeiov1alpha1.Environment) float64 {
		var sum float64
		for _, item := range others {
			sum += metrics.EstimateEnvironmentCost(item, now, rateCard).HourlyUSD
		}
		return sum
	}

	exceedsQuota := func(others []*divergeiov1alpha1.Environment) (bool, string) {
		if maxActive > 0 && len(others)+1 > maxActive {
			return true, fmt.Sprintf("namespace %s quota exceeded: active environments %d exceeds max-active-environments=%d", env.Namespace, len(others)+1, maxActive)
		}
		if maxHourlyCost > 0 {
			totalCost := sumHourlyCost(others) + selfCost
			if totalCost > maxHourlyCost+1e-9 {
				return true, fmt.Sprintf("namespace %s quota exceeded: hourly cost $%.3f/hr exceeds max-hourly-cost-usd=$%.3f/hr", env.Namespace, totalCost, maxHourlyCost)
			}
		}
		return false, ""
	}

	for {
		exceeded, reasonMsg := exceedsQuota(otherActive)
		if !exceeded {
			break
		}
		if policy == QuotaPolicyReject || len(otherActive) == 0 {
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    ConditionQuotaReady,
				Status:  metav1.ConditionFalse,
				Reason:  "NamespaceQuotaExceeded",
				Message: reasonMsg,
			})
			env.Status.Phase = derivePhase(env.Status.Conditions)
			if r.Recorder != nil && r.Recorder.EventRecorder != nil {
				r.Recorder.Event(env, "Warning", "QuotaExceeded", reasonMsg)
			}
			res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, nil, 0)
			return res, true, retErr
		}

		victim := otherActive[0]
		otherActive = otherActive[1:]

		victimBase := victim.DeepCopy()
		if victim.Annotations == nil {
			victim.Annotations = make(map[string]string)
		}
		victim.Annotations[metrics.AnnotationHibernated] = "true"
		if err := r.Patch(ctx, victim, client.MergeFrom(victimBase)); err != nil {
			return ctrl.Result{}, false, fmt.Errorf("failed to hibernate oldest environment %s for quota: %w", victim.Name, err)
		}
		metrics.EstimateEnvironmentCost(victim, now, rateCard)

		if r.Recorder != nil && r.Recorder.EventRecorder != nil {
			r.Recorder.Event(victim, "Normal", "QuotaPreempted",
				fmt.Sprintf("Hibernated to free namespace quota for environment %s", env.Name))
			r.Recorder.Event(env, "Normal", "QuotaPreempted",
				fmt.Sprintf("Hibernated oldest environment %s to satisfy namespace quota", victim.Name))
		}
	}

	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:    ConditionQuotaReady,
		Status:  metav1.ConditionTrue,
		Reason:  "WithinQuota",
		Message: "Namespace FinOps and concurrency quotas satisfied",
	})
	return ctrl.Result{}, false, nil
}
