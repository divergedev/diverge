package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureBannerConfigMap(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &EnvironmentReconciler{
		Client: client,
		Scheme: scheme,
	}

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Branch: "feat/test",
			},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				Banner: &divergeiov1alpha1.BannerSpec{
					Enabled:  true,
					Text:     "Custom Preview",
					Color:    "#00FF00",
					Position: "bottom",
				},
			},
		},
	}

	err := r.ensureBannerConfigMap(context.Background(), env)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	err = client.Get(context.Background(), types.NamespacedName{
		Name:      "diverge-preview-banner",
		Namespace: env.PreviewNamespace(),
	}, cm)
	require.NoError(t, err)

	script, ok := cm.Data["diverge-banner.js"]
	require.True(t, ok)

	assert.True(t, strings.Contains(script, "Custom Preview"))
	assert.True(t, strings.Contains(script, "feat/test"))
	assert.True(t, strings.Contains(script, "#00FF00"))
	assert.True(t, strings.Contains(script, "bottom:0"))
}

func TestEnsureBannerConfigMap_Disabled(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &EnvironmentReconciler{
		Client: client,
		Scheme: scheme,
	}

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Routing: divergeiov1alpha1.EnvironmentRouting{
				Banner: &divergeiov1alpha1.BannerSpec{
					Enabled: false,
				},
			},
		},
	}

	err := r.ensureBannerConfigMap(context.Background(), env)
	require.NoError(t, err)

	cmList := &corev1.ConfigMapList{}
	err = client.List(context.Background(), cmList)
	require.NoError(t, err)
	assert.Len(t, cmList.Items, 0)
}
