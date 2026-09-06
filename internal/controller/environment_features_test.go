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
	"github.com/divergedev/diverge/internal/deployer"
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

	assert.Equal(t, "diverge-features-feat-env", env.Status.FeatureConfigMap)
	assert.Equal(t, "diverge-features-feat-env", env.Status.FeatureEnvVars["DIVERGE_FEATURE_CONFIGMAP"])
	assert.Equal(t, "/etc/diverge/flags/flags.json", env.Status.FeatureEnvVars["FLAGD_FLAG_PATH"])

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

func TestEnvironmentReconciler_TeardownOrder_WaitsForDeployer(t *testing.T) {
	ctx := context.Background()

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "feat-race",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Features: &divergeiov1alpha1.FeatureSpec{
				Provider: "configmap",
				Overrides: map[string]string{
					"checkout_v2": "true",
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{Ready: true, Message: "db ready"}
	r, client, dep, _, _ := newTestReconciler(t, env, dbResult, "https://feat-race.example.com")

	statusBase := env.DeepCopy()
	_, done, err := r.reconcileProvisioning(ctx, env, statusBase)
	require.NoError(t, err)
	assert.False(t, done)

	// Simulate workloads still terminating (Status returns running service)
	dep.status = []deployer.ServiceStatus{
		{Name: "web", Service: "web", Health: "Healthy"},
	}

	// First teardown attempt should wait for deployer workloads
	res, err := r.handleTeardown(ctx, env)
	require.NoError(t, err)
	assert.True(t, res.RequeueAfter > 0, "should requeue while workloads are terminating")

	// Verify ConfigMap is NOT deleted while workloads are still running
	var cm corev1.ConfigMap
	err = client.Get(ctx, types.NamespacedName{Name: "diverge-features-feat-race", Namespace: "default"}, &cm)
	assert.NoError(t, err, "ConfigMap must survive while deployer resources are alive")

	// Now simulate all workloads terminated
	dep.status = nil
	res, err = r.handleTeardown(ctx, env)
	require.NoError(t, err)
	assert.Equal(t, int64(0), res.RequeueAfter.Nanoseconds())

	// ConfigMap is now deleted
	err = client.Get(ctx, types.NamespacedName{Name: "diverge-features-feat-race", Namespace: "default"}, &cm)
	assert.Error(t, err, "ConfigMap should be deleted after deployer confirms 0 resources")
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
