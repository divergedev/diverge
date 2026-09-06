package features

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestBuildFlagdJSON(t *testing.T) {
	overrides := map[string]string{
		"new_checkout": "true",
		"dark_mode":    "false",
		"rate_limit":   "100",
		"threshold":    "99.5",
		"theme":        "obsidian",
	}

	data, err := BuildFlagdJSON(overrides)
	require.NoError(t, err)

	var def FlagdDefinition
	err = json.Unmarshal(data, &def)
	require.NoError(t, err)

	require.Len(t, def.Flags, 5)

	flagBool := def.Flags["new_checkout"]
	assert.Equal(t, "ENABLED", flagBool.State)
	assert.Equal(t, "on", flagBool.DefaultVariant)
	assert.Equal(t, true, flagBool.Variants["on"])
	assert.Equal(t, false, flagBool.Variants["off"])

	flagFalse := def.Flags["dark_mode"]
	assert.Equal(t, "off", flagFalse.DefaultVariant)

	flagInt := def.Flags["rate_limit"]
	assert.Equal(t, "value", flagInt.DefaultVariant)
	assert.Equal(t, float64(100), flagInt.Variants["value"])

	flagFloat := def.Flags["threshold"]
	assert.Equal(t, "value", flagFloat.DefaultVariant)
	assert.Equal(t, 99.5, flagFloat.Variants["value"])

	flagStr := def.Flags["theme"]
	assert.Equal(t, "value", flagStr.DefaultVariant)
	assert.Equal(t, "obsidian", flagStr.Variants["value"])
}

func TestConfigMapProvider_Lifecycle(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	provider := NewConfigMapProvider(c, scheme, logr.Discard())
	assert.Equal(t, "configmap", provider.Type())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-42",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider: "configmap",
				Overrides: map[string]string{
					"checkout_v2": "true",
					"badge":       "preview",
				},
			},
		},
	}

	ctx := context.Background()

	// Initial status before provision
	status, err := provider.Status(ctx, env)
	require.NoError(t, err)
	assert.False(t, status.Ready)

	// Provision
	res, err := provider.Provision(ctx, env)
	require.NoError(t, err)
	assert.Equal(t, "configmap", res.ProviderType)
	assert.Equal(t, "diverge-features-pr-42", res.ConfigMapName)
	assert.Contains(t, res.EnvVars, "DIVERGE_FEATURE_CONFIGMAP")

	// Verify ConfigMap created
	var cm corev1.ConfigMap
	err = c.Get(ctx, types.NamespacedName{Name: "diverge-features-pr-42", Namespace: "default"}, &cm)
	require.NoError(t, err)
	assert.Contains(t, cm.Data, "flags.json")
	assert.Contains(t, cm.Data, "overrides.json")
	assert.Equal(t, "true", cm.Data["checkout_v2"])
	assert.Equal(t, "preview", cm.Data["badge"])

	// Status should now be ready
	status, err = provider.Status(ctx, env)
	require.NoError(t, err)
	assert.True(t, status.Ready)

	// Teardown
	err = provider.Teardown(ctx, env)
	require.NoError(t, err)

	// Verify ConfigMap deleted
	err = c.Get(ctx, types.NamespacedName{Name: "diverge-features-pr-42", Namespace: "default"}, &cm)
	assert.Error(t, err)

	// Idempotent Teardown
	err = provider.Teardown(ctx, env)
	assert.NoError(t, err)
}

func TestConfigMapProvider_SeparateNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	provider := NewConfigMapProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-99",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Namespace: "create",
			},
			Features: &v1alpha1.FeatureSpec{
				Provider: "configmap",
				Overrides: map[string]string{
					"feature_x": "true",
				},
			},
		},
	}

	ctx := context.Background()
	res, err := provider.Provision(ctx, env)
	require.NoError(t, err)
	assert.Equal(t, "diverge-features-pr-99", res.ConfigMapName)

	targetNS := env.PreviewNamespace()
	var cm corev1.ConfigMap
	err = c.Get(ctx, types.NamespacedName{Name: "diverge-features-pr-99", Namespace: targetNS}, &cm)
	require.NoError(t, err)
	assert.Equal(t, targetNS, cm.Namespace)

	err = provider.Teardown(ctx, env)
	require.NoError(t, err)
}
