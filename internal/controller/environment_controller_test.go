package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
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

		assert.Equal(t, "test-env", ns.Labels["diverge.io/environment"])
		assert.Equal(t, "diverge", ns.Labels["diverge.io/managed-by"])
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
		assert.Equal(t, "diverge", limitRange.Labels["diverge.io/managed-by"])
		assert.Len(t, limitRange.Spec.Limits, 1)

		quota := &corev1.ResourceQuota{}
		err = client.Get(ctx, types.NamespacedName{Name: "diverge-preview-quota", Namespace: env.PreviewNamespace()}, quota)
		require.NoError(t, err)
		assert.Equal(t, "diverge", quota.Labels["diverge.io/managed-by"])
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

		assert.Equal(t, "test-env", ns.Labels["diverge.io/environment"])
		assert.Equal(t, "diverge", ns.Labels["diverge.io/managed-by"])
		assert.Equal(t, "custom-value", ns.Labels["custom-label"])
		assert.Equal(t, "ambient", ns.Labels["istio.io/dataplane-mode"])
	})

	t.Run("protects diverge.io labels", func(t *testing.T) {
		client := fake.NewClientBuilder().Build()
		r := &EnvironmentReconciler{Client: client}

		env := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"},
			Spec: divergeiov1alpha1.EnvironmentSpec{
				Deploy: divergeiov1alpha1.EnvironmentDeploy{
					Namespace:       "create",
					NamespaceLabels: map[string]string{"diverge.io/environment": "hacked", "diverge.io/other": "value"},
				},
			},
		}

		err := r.ensureNamespace(ctx, env)
		require.NoError(t, err)

		ns := &corev1.Namespace{}
		err = client.Get(ctx, types.NamespacedName{Name: env.PreviewNamespace()}, ns)
		require.NoError(t, err)

		assert.Equal(t, "test-env", ns.Labels["diverge.io/environment"])
		assert.NotContains(t, ns.Labels, "diverge.io/other")
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
		assert.Equal(t, "test-env", ns.Labels["diverge.io/environment"])
		// The old label is removed because CreateOrUpdate rewrites the map in our implementation
		assert.NotContains(t, ns.Labels, "initial")
	})
}
