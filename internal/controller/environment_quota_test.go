package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sevents "k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/events"
	"github.com/divergedev/diverge/internal/metrics"
)

func buildQuotaTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))
	return scheme
}

func TestNamespaceQuota_MaxActiveHibernateOldestLRU(t *testing.T) {
	ctx := context.Background()
	scheme := buildQuotaTestScheme(t)
	now := time.Now().UTC()

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "team-a",
			Annotations: map[string]string{
				AnnotationMaxActiveEnvironments: "2",
			},
		},
	}

	// env1 created earlier, but received traffic 5 minutes ago
	env1 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-1",
			Namespace:         "team-a",
			CreationTimestamp: metav1.NewTime(now.Add(-3 * time.Hour)),
			Annotations: map[string]string{
				metrics.AnnotationLastTrafficAt: now.Add(-5 * time.Minute).Format(time.RFC3339),
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase: divergeiov1alpha1.PhaseRunning,
		},
	}

	// env2 created later, but last traffic was 90 minutes ago (oldest by LRU!)
	env2 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-2",
			Namespace:         "team-a",
			CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Hour)),
			Annotations: map[string]string{
				metrics.AnnotationLastTrafficAt: now.Add(-90 * time.Minute).Format(time.RFC3339),
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase: divergeiov1alpha1.PhaseRunning,
		},
	}

	// env3 is the new 3rd environment being provisioned
	env3 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-3",
			Namespace:         "team-a",
			CreationTimestamp: metav1.NewTime(now),
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&divergeiov1alpha1.Environment{}).
		WithObjects(ns, env1, env2, env3).
		Build()

	fakeEvents := k8sevents.NewFakeRecorder(20)
	r := &EnvironmentReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: events.NewRecorder(fakeEvents),
	}

	statusBase := env3.DeepCopy()
	_, done, err := r.reconcileNamespaceQuota(ctx, env3, statusBase)
	require.NoError(t, err)
	assert.False(t, done)

	cond := meta.FindStatusCondition(env3.Status.Conditions, ConditionQuotaReady)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, "WithinQuota", cond.Reason)

	// Verify env2 (oldest LRU) was hibernated, while env1 remained active
	var updatedEnv1, updatedEnv2 divergeiov1alpha1.Environment
	require.NoError(t, fakeClient.Get(ctx, types.NamespacedName{Name: "pr-1", Namespace: "team-a"}, &updatedEnv1))
	require.NoError(t, fakeClient.Get(ctx, types.NamespacedName{Name: "pr-2", Namespace: "team-a"}, &updatedEnv2))
	assert.NotEqual(t, "true", updatedEnv1.Annotations[metrics.AnnotationHibernated])
	assert.Equal(t, "true", updatedEnv2.Annotations[metrics.AnnotationHibernated])

	metrics.DeleteEnvironmentCostMetric("pr-1", "team-a")
	metrics.DeleteEnvironmentCostMetric("pr-2", "team-a")
	metrics.DeleteEnvironmentCostMetric("pr-3", "team-a")
}

func TestNamespaceQuota_MaxActiveRejectPolicy(t *testing.T) {
	ctx := context.Background()
	scheme := buildQuotaTestScheme(t)
	now := time.Now().UTC()

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "team-reject",
			Annotations: map[string]string{
				AnnotationMaxActiveEnvironments: "1",
				AnnotationQuotaExceedPolicy:     QuotaPolicyReject,
			},
		},
	}

	env1 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-1",
			Namespace:         "team-reject",
			CreationTimestamp: metav1.NewTime(now.Add(-1 * time.Hour)),
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase: divergeiov1alpha1.PhaseRunning,
		},
	}

	env2 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-2",
			Namespace:         "team-reject",
			CreationTimestamp: metav1.NewTime(now),
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&divergeiov1alpha1.Environment{}).
		WithObjects(ns, env1, env2).
		Build()

	fakeEvents := k8sevents.NewFakeRecorder(20)
	r := &EnvironmentReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: events.NewRecorder(fakeEvents),
	}

	statusBase := env2.DeepCopy()
	_, done, err := r.reconcileNamespaceQuota(ctx, env2, statusBase)
	require.NoError(t, err)
	assert.True(t, done)

	cond := meta.FindStatusCondition(env2.Status.Conditions, ConditionQuotaReady)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "NamespaceQuotaExceeded", cond.Reason)
	assert.Equal(t, divergeiov1alpha1.PhaseFailed, env2.Status.Phase)

	// Existing environment must NOT be hibernated under reject policy
	var updatedEnv1 divergeiov1alpha1.Environment
	require.NoError(t, fakeClient.Get(ctx, types.NamespacedName{Name: "pr-1", Namespace: "team-reject"}, &updatedEnv1))
	assert.NotEqual(t, "true", updatedEnv1.Annotations[metrics.AnnotationHibernated])

	metrics.DeleteEnvironmentCostMetric("pr-1", "team-reject")
	metrics.DeleteEnvironmentCostMetric("pr-2", "team-reject")
}

func TestNamespaceQuota_MaxHourlyCostUSD(t *testing.T) {
	ctx := context.Background()
	scheme := buildQuotaTestScheme(t)
	now := time.Now().UTC()

	// Default service cost in metrics.DefaultCostRateCard():
	// 0.25 vCPU * $0.04 + 0.5 GiB * $0.004 = $0.012/hr per environment.
	// Setting max-hourly-cost-usd to "0.020" allows 1 active environment ($0.012) but not 2 ($0.024).
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "team-budget",
			Annotations: map[string]string{
				AnnotationMaxHourlyCostUSD:  "0.020",
				AnnotationQuotaExceedPolicy: QuotaPolicyHibernateOldest,
			},
		},
	}

	env1 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-budget-1",
			Namespace:         "team-budget",
			CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Hour)),
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase: divergeiov1alpha1.PhaseRunning,
		},
	}

	env2 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-budget-2",
			Namespace:         "team-budget",
			CreationTimestamp: metav1.NewTime(now),
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&divergeiov1alpha1.Environment{}).
		WithObjects(ns, env1, env2).
		Build()

	r := &EnvironmentReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	statusBase := env2.DeepCopy()
	_, done, err := r.reconcileNamespaceQuota(ctx, env2, statusBase)
	require.NoError(t, err)
	assert.False(t, done)

	// env1 should be hibernated to bring total hourly burn within $0.020/hr
	var updatedEnv1 divergeiov1alpha1.Environment
	require.NoError(t, fakeClient.Get(ctx, types.NamespacedName{Name: "pr-budget-1", Namespace: "team-budget"}, &updatedEnv1))
	assert.Equal(t, "true", updatedEnv1.Annotations[metrics.AnnotationHibernated])

	cond := meta.FindStatusCondition(env2.Status.Conditions, ConditionQuotaReady)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)

	metrics.DeleteEnvironmentCostMetric("pr-budget-1", "team-budget")
	metrics.DeleteEnvironmentCostMetric("pr-budget-2", "team-budget")
}

func TestNamespaceQuota_MaxCNPGClustersExceeded(t *testing.T) {
	ctx := context.Background()
	scheme := buildQuotaTestScheme(t)

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "team-cnpg",
			Annotations: map[string]string{
				AnnotationMaxCNPGClusters: "1",
			},
		},
	}

	existingCNPG := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-cnpg-1",
			Namespace: "team-cnpg",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Mode: "snapshot",
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase: divergeiov1alpha1.PhaseRunning,
		},
	}

	newCNPG := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-cnpg-2",
			Namespace: "team-cnpg",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Mode: "fresh",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&divergeiov1alpha1.Environment{}).
		WithObjects(ns, existingCNPG, newCNPG).
		Build()

	r := &EnvironmentReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	statusBase := newCNPG.DeepCopy()
	_, done, err := r.reconcileNamespaceQuota(ctx, newCNPG, statusBase)
	require.NoError(t, err)
	assert.True(t, done)

	cond := meta.FindStatusCondition(newCNPG.Status.Conditions, ConditionQuotaReady)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "MaxCNPGClustersExceeded", cond.Reason)
	assert.Equal(t, divergeiov1alpha1.PhaseFailed, newCNPG.Status.Phase)
}
