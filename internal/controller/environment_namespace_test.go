package controller

import (
	"context"
	"testing"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
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
				Namespace: "same",
			},
		},
	}
	r, client, _, _, _ := newTestReconciler(t, env, nil, "")

	// Create the namespace that matches env.Namespace
	existingNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: env.Namespace,
		},
	}
	require.NoError(t, client.Create(context.Background(), existingNs))

	err := r.ensureNamespace(context.Background(), env)
	require.NoError(t, err)

	ns := &corev1.Namespace{}
	err = client.Get(context.Background(), types.NamespacedName{Name: env.Namespace}, ns)
	require.NoError(t, err)
	assert.Equal(t, env.Namespace, ns.Name)
}

func TestNetworkPolicy_CreatedOnCreateMode(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-netpol",
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

	netpol := &networkingv1.NetworkPolicy{}
	err = client.Get(context.Background(), types.NamespacedName{Name: "diverge-default-netpol", Namespace: env.PreviewNamespace()}, netpol)
	require.NoError(t, err)
	assert.Equal(t, "diverge-default-netpol", netpol.Name)
	assert.Equal(t, env.PreviewNamespace(), netpol.Namespace)
	require.Len(t, netpol.Spec.Egress, 1)
	require.Len(t, netpol.Spec.Egress[0].To, 1)
	require.NotNil(t, netpol.Spec.Egress[0].To[0].IPBlock)
	assert.Equal(t, "0.0.0.0/0", netpol.Spec.Egress[0].To[0].IPBlock.CIDR)
	assert.Contains(t, netpol.Spec.Egress[0].To[0].IPBlock.Except, "169.254.169.254/32")
}

func TestNetworkPolicy_NotCreatedOnSameMode(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-netpol-same",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace: "same",
			},
		},
	}
	r, client, _, _, _ := newTestReconciler(t, env, nil, "")

	err := r.ensureNamespace(context.Background(), env)
	require.NoError(t, err)

	netpol := &networkingv1.NetworkPolicy{}
	err = client.Get(context.Background(), types.NamespacedName{Name: "diverge-default-netpol", Namespace: env.PreviewNamespace()}, netpol)
	require.Error(t, err)
}
