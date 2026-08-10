package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
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
		assert.Len(t, ns.Labels, 2)
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
