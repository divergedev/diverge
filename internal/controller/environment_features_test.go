package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/database"
)

func TestEnvironmentReconciler_FeaturesProvisioning(t *testing.T) {
	ctx := context.Background()

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "feat-env",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Features: &divergeiov1alpha1.FeatureSpec{
				Provider: "configmap",
				Overrides: map[string]string{
					"checkout_v2": "true",
					"beta_badge":  "preview",
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{Ready: true, Message: "db ready"}
	r, client, _, _, _ := newTestReconciler(t, env, dbResult, "https://feat-env.example.com")

	statusBase := env.DeepCopy()
	res, done, err := r.reconcileProvisioning(ctx, env, statusBase)
	require.NoError(t, err)
	assert.False(t, done)
	assert.Equal(t, int64(0), res.RequeueAfter.Nanoseconds())

	cond := meta.FindStatusCondition(env.Status.Conditions, "FeaturesReady")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, "FeaturesProvisioned", cond.Reason)

	var cm corev1.ConfigMap
	err = client.Get(ctx, types.NamespacedName{Name: "diverge-features-feat-env", Namespace: "default"}, &cm)
	require.NoError(t, err)
	assert.Contains(t, cm.Data, "flags.json")
	assert.Equal(t, "true", cm.Data["checkout_v2"])

	// Now verify handleTeardown deletes the ConfigMap
	_, err = r.handleTeardown(ctx, env)
	require.NoError(t, err)

	err = client.Get(ctx, types.NamespacedName{Name: "diverge-features-feat-env", Namespace: "default"}, &cm)
	assert.Error(t, err)
}

func TestEnvironmentReconciler_InvalidFeatureProvider(t *testing.T) {
	ctx := context.Background()

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "feat-invalid",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Features: &divergeiov1alpha1.FeatureSpec{
				Provider: "unregistered-provider",
			},
		},
	}

	dbResult := &database.DatabaseResult{Ready: true, Message: "db ready"}
	r, _, _, _, _ := newTestReconciler(t, env, dbResult, "https://feat-invalid.example.com")

	statusBase := env.DeepCopy()
	_, done, err := r.reconcileProvisioning(ctx, env, statusBase)
	assert.Error(t, err)
	assert.True(t, done)

	cond := meta.FindStatusCondition(env.Status.Conditions, "FeaturesReady")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "FeatureProviderNotFound", cond.Reason)
}
