package controller

import (
	"context"
	"testing"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestEnsureNamespace_CreateMode(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace: "create",
			},
		},
	}
	r, client, _, _, _ := newTestReconciler(t, env, nil, "")

	err := r.ensureNamespace(context.Background(), env)
	require.NoError(t, err)

	nsName := env.PreviewNamespace()
	ns := &corev1.Namespace{}
	err = client.Get(context.Background(), types.NamespacedName{Name: nsName}, ns)
	require.NoError(t, err)
	assert.Equal(t, nsName, ns.Name)
}

func TestEnsureNamespace_ExistingMode(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace: "existing-ns",
			},
		},
	}
	r, client, _, _, _ := newTestReconciler(t, env, nil, "")

	// Create existing namespace manually
	existingNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "existing-ns",
		},
	}
	require.NoError(t, client.Create(context.Background(), existingNs))

	err := r.ensureNamespace(context.Background(), env)
	require.NoError(t, err)

	ns := &corev1.Namespace{}
	err = client.Get(context.Background(), types.NamespacedName{Name: "existing-ns"}, ns)
	require.NoError(t, err)
	assert.Equal(t, "existing-ns", ns.Name)
}
