package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	"github.com/divergedev/diverge/pkg/features"
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

func TestEnvironmentReconciler_FliptProvider(t *testing.T) {
	ctx := context.Background()

	var mu sync.Mutex
	namespaces := make(map[string]bool)
	flags := make(map[string]map[string]interface{})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		path := r.URL.Path
		if path == "/api/v1/namespaces" && r.Method == http.MethodPost {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			key := body["key"].(string)
			namespaces[key] = true
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(body)
			return
		}

		if strings.HasPrefix(path, "/api/v1/namespaces/") && !strings.Contains(path, "/flags") {
			nsKey := strings.TrimPrefix(path, "/api/v1/namespaces/")
			if r.Method == http.MethodGet {
				if namespaces[nsKey] {
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"key": nsKey})
					return
				}
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			if r.Method == http.MethodDelete {
				delete(namespaces, nsKey)
				delete(flags, nsKey)
				w.WriteHeader(http.StatusOK)
				return
			}
		}

		if strings.Contains(path, "/flags") {
			parts := strings.Split(strings.TrimPrefix(path, "/api/v1/namespaces/"), "/")
			nsKey := parts[0]
			if r.Method == http.MethodPost {
				var body map[string]interface{}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if flags[nsKey] == nil {
					flags[nsKey] = make(map[string]interface{})
				}
				flags[nsKey][body["key"].(string)] = body
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(body)
				return
			}
		}

		http.NotFound(w, r)
	}))
	defer ts.Close()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flipt-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"url":   []byte(ts.URL),
			"token": []byte("reconciler-token"),
		},
	}

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "feat-flipt-env",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Features: &divergeiov1alpha1.FeatureSpec{
				Provider:      "flipt",
				ConnectionRef: "flipt-secret",
				Overrides: map[string]string{
					"flag_alpha": "true",
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{Ready: true, Message: "db ready"}
	r, client, _, _, _ := newTestReconciler(t, env, dbResult, "https://feat-flipt-env.example.com")
	err := client.Create(ctx, secret)
	require.NoError(t, err)

	statusBase := env.DeepCopy()
	res, done, err := r.reconcileProvisioning(ctx, env, statusBase)
	require.NoError(t, err)
	assert.False(t, done)
	assert.Equal(t, int64(0), res.RequeueAfter.Nanoseconds())

	cond := meta.FindStatusCondition(env.Status.Conditions, "FeaturesReady")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, "FeaturesProvisioned", cond.Reason)

	assert.Equal(t, ts.URL, env.Status.FeatureEnvVars["FLIPT_URL"])
	assert.Equal(t, "diverge-feat-flipt-env", env.Status.FeatureEnvVars["FLIPT_NAMESPACE"])
	assert.Equal(t, "reconciler-token", env.Status.FeatureEnvVars["FLIPT_AUTH_TOKEN"])
	assert.Equal(t, "", env.Status.FeatureConfigMap)

	mu.Lock()
	assert.True(t, namespaces["diverge-feat-flipt-env"])
	assert.NotNil(t, flags["diverge-feat-flipt-env"]["flag_alpha"])
	mu.Unlock()

	// Teardown deletes the remote namespace
	_, err = r.handleTeardown(ctx, env)
	require.NoError(t, err)

	mu.Lock()
	assert.False(t, namespaces["diverge-feat-flipt-env"])
	mu.Unlock()
}

// TestEnvironmentReconciler_Flagsmith tests end-to-end reconciliation, identity provisioning, and teardown for Flagsmith.
func TestEnvironmentReconciler_Flagsmith(t *testing.T) {
	ctx := context.Background()

	var (
		mu         sync.Mutex
		identities = make(map[string]bool)
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if strings.Contains(r.URL.Path, "/featurestates/") {
			assert.Equal(t, http.MethodPost, r.Method)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"enabled":true}`))
			return
		}
		if strings.Contains(r.URL.Path, "/identities/") {
			if r.Method == http.MethodPost {
				var body struct {
					Identifier string `json:"identifier"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.Identifier != "" {
					identities[body.Identifier] = true
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"identifier":%q}`, body.Identifier)
				return
			}
			if r.Method == http.MethodDelete {
				for id := range identities {
					if strings.Contains(r.URL.Path, id) {
						delete(identities, id)
						break
					}
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flagsmith-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"url":            []byte(ts.URL),
			"environmentKey": []byte("flagsmith-env-key"),
		},
	}

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "feat-flagsmith-env",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Features: &divergeiov1alpha1.FeatureSpec{
				Provider:      "flagsmith",
				ConnectionRef: "flagsmith-secret",
				Overrides: map[string]string{
					"checkout_v2": "true",
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{Ready: true, Message: "db ready"}
	r, client, _, _, _ := newTestReconciler(t, env, dbResult, "https://feat-flagsmith-env.example.com")
	err := client.Create(ctx, secret)
	require.NoError(t, err)

	statusBase := env.DeepCopy()
	_, done, err := r.reconcileProvisioning(ctx, env, statusBase)
	require.NoError(t, err)
	assert.False(t, done)

	cond := meta.FindStatusCondition(env.Status.Conditions, "FeaturesReady")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, "flagsmith-env-key", env.Status.FeatureEnvVars["FLAGSMITH_ENVIRONMENT_KEY"])
	assert.Equal(t, features.FlagsmithIdentityName(env.Namespace, env.Name), env.Status.FeatureEnvVars["FLAGSMITH_IDENTITY"])
	assert.Equal(t, ts.URL, env.Status.FeatureEnvVars["FLAGSMITH_API_URL"])

	mu.Lock()
	assert.Len(t, identities, 1)
	mu.Unlock()

	_, err = r.handleTeardown(ctx, env)
	require.NoError(t, err)

	mu.Lock()
	assert.Empty(t, identities)
	mu.Unlock()
}

func TestEnvironmentReconciler_UnleashStub(t *testing.T) {
	ctx := context.Background()

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "feat-unleash-env",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Features: &divergeiov1alpha1.FeatureSpec{
				Provider: "unleash",
			},
		},
	}

	dbResult := &database.DatabaseResult{Ready: true, Message: "db ready"}
	r, _, _, _, _ := newTestReconciler(t, env, dbResult, "https://feat-unleash-env.example.com")

	statusBase := env.DeepCopy()
	_, done, err := r.reconcileProvisioning(ctx, env, statusBase)
	require.NoError(t, err)
	assert.False(t, done)

	cond := meta.FindStatusCondition(env.Status.Conditions, "FeaturesReady")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, features.UnleashEnvironmentName("default", "feat-unleash-env"), env.Status.FeatureEnvVars["UNLEASH_APP_NAME"])
	assert.Equal(t, "feat-unleash-env", env.Status.FeatureEnvVars["UNLEASH_ENVIRONMENT"])

	_, err = r.handleTeardown(ctx, env)
	require.NoError(t, err)
}
