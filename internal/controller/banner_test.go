package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureBannerConfigMap_CreateMode(t *testing.T) {
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
			UID:       "12345",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Branch: "feat/test",
			},
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace: "create",
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

	// OwnerReference should NOT be set across namespaces
	assert.Empty(t, cm.OwnerReferences)

	script, ok := cm.Data["diverge-banner.js"]
	require.True(t, ok)

	assert.True(t, strings.Contains(script, "Custom Preview"))
	assert.True(t, strings.Contains(script, "feat/test"))
	assert.True(t, strings.Contains(script, "#00FF00"))
	assert.True(t, strings.Contains(script, `"bottom"`))
}

func TestEnsureBannerConfigMap_SameMode_OwnerReference(t *testing.T) {
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
			UID:       "12345",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Branch: "feat/test",
			},
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace: "same",
			},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				Banner: &divergeiov1alpha1.BannerSpec{
					Enabled: true,
				},
			},
		},
	}

	err := r.ensureBannerConfigMap(context.Background(), env)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	err = client.Get(context.Background(), types.NamespacedName{
		Name:      "diverge-preview-banner",
		Namespace: "default",
	}, cm)
	require.NoError(t, err)

	require.Len(t, cm.OwnerReferences, 1)
	assert.Equal(t, "Environment", cm.OwnerReferences[0].Kind)
	assert.Equal(t, "test-env", cm.OwnerReferences[0].Name)
}

func TestEnsureBannerConfigMap_XSS(t *testing.T) {
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
				Branch: "feat/xss'; alert(1); //",
			},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				Banner: &divergeiov1alpha1.BannerSpec{
					Enabled: true,
				},
			},
		},
	}

	err := r.ensureBannerConfigMap(context.Background(), env)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	err = client.Get(context.Background(), types.NamespacedName{
		Name:      "diverge-preview-banner",
		Namespace: "default",
	}, cm)
	require.NoError(t, err)

	script := cm.Data["diverge-banner.js"]

	// The branch name should be JSON-encoded, so the single quotes and semi-colons won't break out of a JSON string.
	// We'll verify it's safely embedded within JSON and doesn't look like an unescaped raw injection.
	assert.False(t, strings.Contains(script, `branch = 'feat/xss'; alert(1); //'`))
	assert.True(t, strings.Contains(script, `"feat/xss'; alert(1); //"`))
}

func TestBannerReconcileCleanup(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-env",
			Namespace:  "default",
			Generation: 2,
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace: "create",
			},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				Banner: &divergeiov1alpha1.BannerSpec{
					Enabled: false,
				},
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			ObservedGeneration: 1,
		},
	}

	// Pre-create the configmap
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-preview-banner",
			Namespace: env.PreviewNamespace(),
		},
		Data: map[string]string{"diverge-banner.js": "console.log();"},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(env, cm).
		WithStatusSubresource(env).Build()

	r := &EnvironmentReconciler{
		Client: client,
		Scheme: scheme,
	}

	err := r.ensureBannerConfigMap(context.Background(), env)
	require.NoError(t, err)

	// Verify the ConfigMap is deleted
	err = client.Get(context.Background(), types.NamespacedName{
		Name:      "diverge-preview-banner",
		Namespace: env.PreviewNamespace(),
	}, &corev1.ConfigMap{})
	assert.True(t, apierrors.IsNotFound(err), "Expected ConfigMap to be deleted")
}
