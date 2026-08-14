package routing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestTemporalProvider_Reconcile(t *testing.T) {
	c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if patch.Type() == types.ApplyPatchType {
				return cl.Create(ctx, obj)
			}
			return cl.Patch(ctx, obj, patch, opts...)
		},
	}).Build()
	p := &TemporalProvider{Client: c}

	env := &v1alpha1.Environment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       "Environment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "test-ns",
			UID:       "test-uid",
		},
	}

	err := p.Reconcile(context.Background(), env)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	err = c.Get(context.Background(), types.NamespacedName{Name: "diverge-temporal-test-uid", Namespace: "test-ns"}, cm)
	require.NoError(t, err)

	assert.Equal(t, "test-env", cm.Data["diverge-env"])
	assert.Equal(t, "test-env", cm.Data["task-queue-suffix"])
	assert.Equal(t, "diverge", cm.Labels["diverge.io/managed-by"])
	assert.Equal(t, "test-env", cm.Labels["diverge.io/environment"])
	assert.Len(t, cm.OwnerReferences, 1)
	assert.Equal(t, "Environment", cm.OwnerReferences[0].Kind)
	assert.Equal(t, "test-env", cm.OwnerReferences[0].Name)
}

func TestTemporalProvider_Teardown(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-temporal-test-uid",
			Namespace: "test-ns",
			Labels: map[string]string{
				"diverge.io/managed-by":  "diverge",
				"diverge.io/environment": "test-env",
			},
		},
		Data: map[string]string{
			"diverge-env":       "test-env",
			"task-queue-suffix": "test-env",
		},
	}

	c := fake.NewClientBuilder().WithObjects(cm).Build()
	p := &TemporalProvider{Client: c}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "test-ns",
		},
	}

	err := p.Teardown(context.Background(), env)
	require.NoError(t, err)

	var list corev1.ConfigMapList
	err = c.List(context.Background(), &list, client.InNamespace("test-ns"))
	require.NoError(t, err)
	assert.Empty(t, list.Items)
}
