package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/metrics"
)

func TestEnsureNamespace(t *testing.T) {
	ctx := context.Background()

	t.Run("creates namespace with default labels", func(t *testing.T) {
		client := fake.NewClientBuilder().Build()
		r := &EnvironmentReconciler{Client: client}

		env := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"},
			Spec: divergeiov1alpha1.EnvironmentSpec{
				Deploy: divergeiov1alpha1.EnvironmentDeploy{
					Namespace: "create",
				},
			},
		}

		err := r.ensureNamespace(ctx, env)
		require.NoError(t, err)

		ns := &corev1.Namespace{}
		err = client.Get(ctx, types.NamespacedName{Name: env.PreviewNamespace()}, ns)
		require.NoError(t, err)

		assert.Equal(t, "test-env", ns.Labels["divergedev.com/environment"])
		assert.Equal(t, "diverge", ns.Labels["divergedev.com/managed-by"])
		assert.Equal(t, "restricted", ns.Labels["pod-security.kubernetes.io/enforce"])
		assert.Equal(t, "latest", ns.Labels["pod-security.kubernetes.io/enforce-version"])
		assert.Equal(t, "restricted", ns.Labels["pod-security.kubernetes.io/warn"])
		assert.Equal(t, "latest", ns.Labels["pod-security.kubernetes.io/warn-version"])
		assert.Equal(t, "restricted", ns.Labels["pod-security.kubernetes.io/audit"])
		assert.Equal(t, "latest", ns.Labels["pod-security.kubernetes.io/audit-version"])
		assert.Len(t, ns.Labels, 8)
	})

	t.Run("injects limit range and resource quota", func(t *testing.T) {
		client := fake.NewClientBuilder().Build()
		r := &EnvironmentReconciler{Client: client}

		env := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"},
			Spec: divergeiov1alpha1.EnvironmentSpec{
				Deploy: divergeiov1alpha1.EnvironmentDeploy{
					Namespace: "create",
				},
			},
		}

		err := r.ensureNamespace(ctx, env)
		require.NoError(t, err)

		limitRange := &corev1.LimitRange{}
		err = client.Get(ctx, types.NamespacedName{Name: "diverge-default-limits", Namespace: env.PreviewNamespace()}, limitRange)
		require.NoError(t, err)
		assert.Equal(t, "diverge", limitRange.Labels["divergedev.com/managed-by"])
		assert.Len(t, limitRange.Spec.Limits, 1)

		quota := &corev1.ResourceQuota{}
		err = client.Get(ctx, types.NamespacedName{Name: "diverge-preview-quota", Namespace: env.PreviewNamespace()}, quota)
		require.NoError(t, err)
		assert.Equal(t, "diverge", quota.Labels["divergedev.com/managed-by"])
		assert.Equal(t, "5", quota.Spec.Hard.Pods().String())
	})

	t.Run("does not overwrite existing limit range and resource quota", func(t *testing.T) {
		client := fake.NewClientBuilder().Build()
		r := &EnvironmentReconciler{Client: client}

		env := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"},
			Spec: divergeiov1alpha1.EnvironmentSpec{
				Deploy: divergeiov1alpha1.EnvironmentDeploy{
					Namespace: "create",
				},
			},
		}

		existingLimitRange := &corev1.LimitRange{
			ObjectMeta: metav1.ObjectMeta{Name: "diverge-default-limits", Namespace: env.PreviewNamespace()},
			Spec: corev1.LimitRangeSpec{
				Limits: []corev1.LimitRangeItem{{
					Type: corev1.LimitTypeContainer,
					Default: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("999Mi"),
					},
				}},
			},
		}
		require.NoError(t, client.Create(ctx, existingLimitRange))

		existingQuota := &corev1.ResourceQuota{
			ObjectMeta: metav1.ObjectMeta{Name: "diverge-preview-quota", Namespace: env.PreviewNamespace()},
			Spec: corev1.ResourceQuotaSpec{
				Hard: corev1.ResourceList{
					corev1.ResourcePods: resource.MustParse("99"),
				},
			},
		}
		require.NoError(t, client.Create(ctx, existingQuota))

		err := r.ensureNamespace(ctx, env)
		require.NoError(t, err)

		limitRange := &corev1.LimitRange{}
		err = client.Get(ctx, types.NamespacedName{Name: "diverge-default-limits", Namespace: env.PreviewNamespace()}, limitRange)
		require.NoError(t, err)
		assert.Equal(t, "999Mi", limitRange.Spec.Limits[0].Default.Memory().String())

		quota := &corev1.ResourceQuota{}
		err = client.Get(ctx, types.NamespacedName{Name: "diverge-preview-quota", Namespace: env.PreviewNamespace()}, quota)
		require.NoError(t, err)
		assert.Equal(t, "99", quota.Spec.Hard.Pods().String())
	})

	t.Run("merges user labels", func(t *testing.T) {
		client := fake.NewClientBuilder().Build()
		r := &EnvironmentReconciler{Client: client}

		env := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"},
			Spec: divergeiov1alpha1.EnvironmentSpec{
				Deploy: divergeiov1alpha1.EnvironmentDeploy{
					Namespace:       "create",
					NamespaceLabels: map[string]string{"custom-label": "custom-value", "istio.io/dataplane-mode": "ambient"},
				},
			},
		}

		err := r.ensureNamespace(ctx, env)
		require.NoError(t, err)

		ns := &corev1.Namespace{}
		err = client.Get(ctx, types.NamespacedName{Name: env.PreviewNamespace()}, ns)
		require.NoError(t, err)

		assert.Equal(t, "test-env", ns.Labels["divergedev.com/environment"])
		assert.Equal(t, "diverge", ns.Labels["divergedev.com/managed-by"])
		assert.Equal(t, "custom-value", ns.Labels["custom-label"])
		assert.Equal(t, "ambient", ns.Labels["istio.io/dataplane-mode"])
	})

	t.Run("protects divergedev.com labels", func(t *testing.T) {
		client := fake.NewClientBuilder().Build()
		r := &EnvironmentReconciler{Client: client}

		env := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"},
			Spec: divergeiov1alpha1.EnvironmentSpec{
				Deploy: divergeiov1alpha1.EnvironmentDeploy{
					Namespace:       "create",
					NamespaceLabels: map[string]string{"divergedev.com/environment": "hacked", "divergedev.com/other": "value"},
				},
			},
		}

		err := r.ensureNamespace(ctx, env)
		require.NoError(t, err)

		ns := &corev1.Namespace{}
		err = client.Get(ctx, types.NamespacedName{Name: env.PreviewNamespace()}, ns)
		require.NoError(t, err)

		assert.Equal(t, "test-env", ns.Labels["divergedev.com/environment"])
		assert.NotContains(t, ns.Labels, "divergedev.com/other")
	})

	t.Run("updates labels on re-reconcile", func(t *testing.T) {
		client := fake.NewClientBuilder().Build()
		r := &EnvironmentReconciler{Client: client}

		env := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"},
			Spec: divergeiov1alpha1.EnvironmentSpec{
				Deploy: divergeiov1alpha1.EnvironmentDeploy{
					Namespace:       "create",
					NamespaceLabels: map[string]string{"initial": "value"},
				},
			},
		}

		err := r.ensureNamespace(ctx, env)
		require.NoError(t, err)

		// Re-reconcile with new labels
		env.Spec.Deploy.NamespaceLabels = map[string]string{"updated": "new-value"}
		err = r.ensureNamespace(ctx, env)
		require.NoError(t, err)

		ns := &corev1.Namespace{}
		err = client.Get(ctx, types.NamespacedName{Name: env.PreviewNamespace()}, ns)
		require.NoError(t, err)

		assert.Equal(t, "new-value", ns.Labels["updated"])
		assert.Equal(t, "test-env", ns.Labels["divergedev.com/environment"])
		// The old label is removed because CreateOrUpdate rewrites the map in our implementation
		assert.NotContains(t, ns.Labels, "initial")
	})
}

func TestReconcile_CostAndStuckTTLMetrics(t *testing.T) {
	pastExpiry := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stuck-env",
			Namespace: "default",
			Annotations: map[string]string{
				"divergedev.com/traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"api"},
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase:     divergeiov1alpha1.PhaseRunning,
			ExpiresAt: &pastExpiry,
		},
	}

	r, _, _, _, _ := newTestReconciler(t, env, nil, "")
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "stuck-env", Namespace: "default"}}

	// Reconcile adds finalizer and runs deferred metrics update
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	ttlRemaining := testutil.ToFloat64(metrics.EnvironmentTTLRemaining.WithLabelValues("stuck-env", "default"))
	assert.Less(t, ttlRemaining, 0.0, "stuck Running environment past ExpiresAt must export negative TTL remaining")

	hourlyCost := testutil.ToFloat64(metrics.EnvironmentEstimatedHourlyCostUSD.WithLabelValues("stuck-env", "default"))
	assert.Greater(t, hourlyCost, 0.0)

	metrics.EnvironmentTTLRemaining.DeleteLabelValues("stuck-env", "default")
	metrics.DeleteEnvironmentCostMetric("stuck-env", "default")
}

func TestReconcileLifecycle_IdleHibernation(t *testing.T) {
	created := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "idle-env",
			Namespace:         "default",
			CreationTimestamp: created,
			Annotations: map[string]string{
				metrics.AnnotationHibernateAfter: "30m",
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase:     divergeiov1alpha1.PhaseRunning,
			CreatedAt: &created,
		},
	}

	r, _, _, _, _ := newTestReconciler(t, env, nil, "")
	statusBase := env.DeepCopy()
	_, _, _, err := r.reconcileLifecycle(context.Background(), env, statusBase, divergeiov1alpha1.PhaseRunning)
	require.NoError(t, err)
	assert.Equal(t, "true", env.Annotations[metrics.AnnotationHibernated])
	assert.Equal(t, 0.0, testutil.ToFloat64(metrics.EnvironmentEstimatedHourlyCostUSD.WithLabelValues("idle-env", "default")))

	metrics.DeleteEnvironmentCostMetric("idle-env", "default")
}

func TestHandleTeardown_ForceTeardownEscapeHatch(t *testing.T) {
	t.Run("force-teardown annotation bypasses errors", func(t *testing.T) {
		now := metav1.Now()
		env := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "force-env",
				Namespace:         "default",
				Finalizers:        []string{environmentFinalizer},
				DeletionTimestamp: &now,
				Annotations: map[string]string{
					"divergedev.com/force-teardown": "true",
				},
			},
		}
		r, _, dep, _, db := newTestReconciler(t, env, nil, "")
		dep.teardownErr = errors.New("deployer unreachable")
		db.teardownErr = errors.New("db unreachable")

		res, err := r.handleTeardown(context.Background(), env)
		require.NoError(t, err)
		assert.Empty(t, res)
	})

	t.Run("deletion timeout over 15m bypasses errors", func(t *testing.T) {
		expiredDel := metav1.NewTime(time.Now().Add(-20 * time.Minute))
		env := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "wedged-env",
				Namespace:         "default",
				Finalizers:        []string{environmentFinalizer},
				DeletionTimestamp: &expiredDel,
			},
		}
		r, _, _, _, db := newTestReconciler(t, env, nil, "")
		db.teardownErr = errors.New("db unreachable")

		res, err := r.handleTeardown(context.Background(), env)
		require.NoError(t, err)
		assert.Empty(t, res)
	})
}

func TestHookJobAndPreviewGroup_TraceparentPropagation(t *testing.T) {
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "traced-env",
			UID:  "uid-1",
			Annotations: map[string]string{
				"divergedev.com/traceparent": tp,
			},
		},
	}

	job := buildJob(HookJobConfig{
		JobName:   "traced-hook",
		Namespace: "default",
		Image:     "alpine:latest",
		Owner:     env,
	})
	require.Len(t, job.Spec.Template.Spec.Containers, 1)
	assert.Contains(t, job.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
		Name:  "TRACEPARENT",
		Value: tp,
	})

	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "traced-pg",
			Annotations: map[string]string{
				"divergedev.com/traceparent": tp,
			},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Routing: divergeiov1alpha1.PreviewGroupRouting{
				HeaderValue: "traced-pg",
			},
		},
	}
	pgReconciler := &PreviewGroupReconciler{}
	child := pgReconciler.buildChildEnvironment(pg, divergeiov1alpha1.PreviewGroupServiceSpec{Name: "api"}, "child-api", "default")
	assert.Equal(t, tp, child.Annotations["divergedev.com/traceparent"])
}
