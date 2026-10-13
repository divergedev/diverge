package features

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type mockUnleashServer struct {
	mu              sync.Mutex
	features        map[string]UnleashFeature
	enabledFeatures map[string]bool
	strategies      map[string][]UnleashStrategy // featureName -> strategies
	reqAdminToken   string
	forceHealthErr  bool
	nextStratID     int
}

func newMockUnleashServer(reqAdminToken string) (*httptest.Server, *mockUnleashServer) {
	mock := &mockUnleashServer{
		features:        make(map[string]UnleashFeature),
		enabledFeatures: make(map[string]bool),
		strategies:      make(map[string][]UnleashStrategy),
		reqAdminToken:   reqAdminToken,
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		defer mock.mu.Unlock()

		path := r.URL.Path

		// Health check
		if path == "/internal-backstage/health" {
			if mock.forceHealthErr {
				http.Error(w, `{"health":"BAD"}`, http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"health":"GOOD"}`))
			return
		}

		// Verify Authorization header when required
		if mock.reqAdminToken != "" && r.Header.Get("Authorization") != mock.reqAdminToken {
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		// Parse /api/admin/projects/{project}/...
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) >= 4 && parts[0] == "api" && parts[1] == "admin" && parts[2] == "projects" {
			// GET /api/admin/projects/{project}
			if len(parts) == 4 && r.Method == http.MethodGet {
				if mock.forceHealthErr {
					http.Error(w, `{"message":"error"}`, http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"name":"default"}`))
				return
			}

			// /api/admin/projects/{project}/features
			if len(parts) == 5 && parts[4] == "features" {
				switch r.Method {
				case http.MethodGet:
					list := make([]UnleashFeature, 0, len(mock.features))
					for _, f := range mock.features {
						list = append(list, f)
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"features": list})
					return
				case http.MethodPost:
					var f UnleashFeature
					if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					mock.features[f.Name] = f
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(f)
					return
				}
			}

			// /api/admin/projects/{project}/features/{featureName}
			if len(parts) == 6 && parts[4] == "features" && r.Method == http.MethodGet {
				featureName := parts[5]
				f, exists := mock.features[featureName]
				if !exists {
					http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(f)
				return
			}

			// POST /api/admin/projects/{project}/features/{featureName}/environments/{env}/on
			if len(parts) == 9 && parts[4] == "features" && parts[6] == "environments" && parts[8] == "on" && r.Method == http.MethodPost {
				featureName := parts[5]
				mock.enabledFeatures[featureName] = true
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
				return
			}

			// /api/admin/projects/{project}/features/{featureName}/environments/{env}/strategies
			if len(parts) >= 9 && parts[4] == "features" && parts[6] == "environments" && parts[8] == "strategies" {
				featureName := parts[5]

				if len(parts) == 9 {
					switch r.Method {
					case http.MethodGet:
						strats := mock.strategies[featureName]
						if strats == nil {
							strats = []UnleashStrategy{}
						}
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_ = json.NewEncoder(w).Encode(strats)
						return
					case http.MethodPost:
						var s UnleashStrategy
						if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
							http.Error(w, err.Error(), http.StatusBadRequest)
							return
						}
						mock.nextStratID++
						s.ID = fmt.Sprintf("strat-%d", mock.nextStratID)
						mock.strategies[featureName] = append(mock.strategies[featureName], s)
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_ = json.NewEncoder(w).Encode(s)
						return
					}
				}

				if len(parts) == 10 {
					stratID := parts[9]
					if stratID == "set-strategies-sort-order" && r.Method == http.MethodPut {
						w.WriteHeader(http.StatusOK)
						return
					}
					switch r.Method {
					case http.MethodPut:
						var s UnleashStrategy
						if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
							http.Error(w, err.Error(), http.StatusBadRequest)
							return
						}
						s.ID = stratID
						existing := mock.strategies[featureName]
						for i := range existing {
							if existing[i].ID == stratID {
								existing[i] = s
								break
							}
						}
						mock.strategies[featureName] = existing
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_ = json.NewEncoder(w).Encode(s)
						return
					case http.MethodDelete:
						existing := mock.strategies[featureName]
						filtered := make([]UnleashStrategy, 0, len(existing))
						found := false
						for _, s := range existing {
							if s.ID == stratID {
								found = true
								continue
							}
							filtered = append(filtered, s)
						}
						mock.strategies[featureName] = filtered
						if !found {
							http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
							return
						}
						w.WriteHeader(http.StatusOK)
						return
					}
				}
			}
		}

		http.Error(w, fmt.Sprintf("unhandled path: %s", path), http.StatusNotFound)
	}))

	return ts, mock
}

func TestUnleashProvider_Type(t *testing.T) {
	provider := NewUnleashProvider(nil, nil, logr.Discard())
	assert.Equal(t, "unleash", provider.Type())
}

func TestUnleashStrategyTitle(t *testing.T) {
	assert.Contains(t, UnleashStrategyTitle("default", "my-preview"), "diverge-my-preview-")
	assert.Contains(t, UnleashStrategyTitle("tenant-a", "feature-1"), "diverge-tenant-a-feature-1-")

	// Distinct namespaces produce distinct strategy titles
	t1 := UnleashStrategyTitle("ns1", "my-env")
	t2 := UnleashStrategyTitle("ns2", "my-env")
	assert.NotEqual(t, t1, t2)

	// Length bounds check
	longName := strings.Repeat("a", 100)
	title := UnleashStrategyTitle("long-namespace", longName)
	assert.True(t, strings.HasPrefix(title, "diverge-"))
	assert.LessOrEqual(t, len(title), 63)
}

func TestUnleashProvider_Provision_Tier1Secret(t *testing.T) {
	ts, mock := newMockUnleashServer("tier1-admin-token")
	defer ts.Close()

	// Seed a baseline strategy on dark_mode to verify false override adds NOT_IN exclusion constraint
	mock.features["dark_mode"] = UnleashFeature{Name: "dark_mode", Type: "release"}
	mock.strategies["dark_mode"] = []UnleashStrategy{
		{
			ID:         "baseline-strat",
			Name:       "flexibleRollout",
			Title:      "baseline-rollout",
			Parameters: map[string]string{"rollout": "100"},
		},
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-unleash-secret",
			Namespace: "app-ns",
		},
		Data: map[string][]byte{
			"url":         []byte(ts.URL),
			"adminToken":  []byte("tier1-admin-token"),
			"clientToken": []byte("tier1-client-token"),
			"project":     []byte("default"),
			"environment": []byte("development"),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()
	provider := NewUnleashProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-42",
			Namespace: "app-ns",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "unleash",
				ConnectionRef: "my-unleash-secret",
				Overrides: map[string]string{
					"checkout_v2": "true",
					"dark_mode":   "false",
					"theme":       "solarized",
				},
			},
		},
	}

	ctx := context.Background()

	// Provision
	res, err := provider.Provision(ctx, env)
	require.NoError(t, err)
	require.NotNil(t, res)
	expectedTitle := UnleashStrategyTitle(env.Namespace, env.Name)
	assert.Equal(t, "unleash", res.ProviderType)
	assert.Equal(t, ts.URL+"/api", res.EnvVars["UNLEASH_URL"])
	assert.Equal(t, ts.URL+"/api", res.EnvVars["OPENFEATURE_UNLEASH_URL"])
	assert.Equal(t, "tier1-client-token", res.EnvVars["UNLEASH_API_TOKEN"])
	assert.Equal(t, "default", res.EnvVars["UNLEASH_PROJECT"])
	assert.Equal(t, "development", res.EnvVars["UNLEASH_ENVIRONMENT"])
	assert.Equal(t, expectedTitle, res.EnvVars["UNLEASH_STRATEGY_TITLE"])
	assert.Equal(t, expectedTitle, res.EnvVars["OPENFEATURE_TARGET_KEY"])
	assert.Equal(t, "checkout_v2,dark_mode,theme", env.Annotations[UnleashManagedOverridesAnnotation])

	// Verify strategies and environment enablement in mock server
	mock.mu.Lock()
	assert.Contains(t, mock.features, "checkout_v2")
	assert.True(t, mock.enabledFeatures["checkout_v2"])
	assert.Contains(t, mock.features, "dark_mode")
	assert.True(t, mock.enabledFeatures["dark_mode"], "existing feature must also be enabled in environment")
	assert.Contains(t, mock.features, "theme")
	assert.True(t, mock.enabledFeatures["theme"])

	checkoutStrats := mock.strategies["checkout_v2"]
	require.Len(t, checkoutStrats, 1)
	assert.Equal(t, expectedTitle, checkoutStrats[0].Title)
	assert.Equal(t, "100", checkoutStrats[0].Parameters["rollout"])
	assert.False(t, checkoutStrats[0].Disabled)

	darkStrats := mock.strategies["dark_mode"]
	require.Len(t, darkStrats, 2)
	// Baseline strategy must have diverge.environment NOT_IN ["pr-42"] exclusion constraint
	assert.Equal(t, "baseline-strat", darkStrats[0].ID)
	require.Len(t, darkStrats[0].Constraints, 1)
	assert.Equal(t, "diverge.environment", darkStrats[0].Constraints[0].ContextName)
	assert.Equal(t, "NOT_IN", darkStrats[0].Constraints[0].Operator)
	assert.Equal(t, []string{"pr-42"}, darkStrats[0].Constraints[0].Values)
	// Preview strategy is disabled with rollout 0
	assert.Equal(t, "0", darkStrats[1].Parameters["rollout"])
	assert.True(t, darkStrats[1].Disabled)

	themeStrats := mock.strategies["theme"]
	require.Len(t, themeStrats, 1)
	require.Len(t, themeStrats[0].Variants, 1)
	assert.Equal(t, "solarized", themeStrats[0].Variants[0].Payload.Value)
	mock.mu.Unlock()

	// Remove "theme" from overrides and re-provision -> stale strategy on "theme" must be deleted
	delete(env.Spec.Features.Overrides, "theme")
	_, err = provider.Provision(ctx, env)
	require.NoError(t, err)
	assert.Equal(t, "checkout_v2,dark_mode", env.Annotations[UnleashManagedOverridesAnnotation])
	mock.mu.Lock()
	assert.Empty(t, mock.strategies["theme"])
	mock.mu.Unlock()

	// Status check
	status, err := provider.Status(ctx, env)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.True(t, status.Ready)

	// Remove "checkout_v2" from overrides before Teardown -> Teardown still scans all project features and removes it
	delete(env.Spec.Features.Overrides, "checkout_v2")
	err = provider.Teardown(ctx, env)
	require.NoError(t, err)

	mock.mu.Lock()
	assert.Empty(t, mock.strategies["checkout_v2"])
	require.Len(t, mock.strategies["dark_mode"], 1)
	assert.Equal(t, "baseline-strat", mock.strategies["dark_mode"][0].ID)
	assert.Empty(t, mock.strategies["dark_mode"][0].Constraints, "exclusion constraint must be cleaned up on teardown")
	assert.Empty(t, mock.strategies["theme"])
	mock.mu.Unlock()
}

func TestUnleashProvider_Provision_Tier2AndDefaultSecrets(t *testing.T) {
	ts, _ := newMockUnleashServer("shared-admin-token")
	defer ts.Close()

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	// Secret in diverge-system without clientToken: adminToken must NOT be leaked to workload EnvVars
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash-connection",
			Namespace: "diverge-system",
		},
		Data: map[string][]byte{
			"UNLEASH_URL":         []byte(ts.URL),
			"UNLEASH_ADMIN_TOKEN": []byte("shared-admin-token"),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()
	provider := NewUnleashProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "feat-x",
			Namespace: "tenant-ns",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider: "unleash",
			},
		},
	}

	res, err := provider.Provision(context.Background(), env)
	require.NoError(t, err)
	assert.Empty(t, res.EnvVars["UNLEASH_API_TOKEN"], "adminToken must never be sent to preview workloads")
	assert.Empty(t, res.EnvVars["OPENFEATURE_UNLEASH_API_TOKEN"], "adminToken must never be sent to preview workloads")
	assert.Equal(t, UnleashStrategyTitle(env.Namespace, env.Name), res.EnvVars["UNLEASH_STRATEGY_TITLE"])
}

func TestUnleashProvider_TenantSecretURLDoesNotUseEnvVarTokens(t *testing.T) {
	var receivedAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"features":[]}`))
	}))
	defer ts.Close()

	t.Setenv("DIVERGE_UNLEASH_ADMIN_TOKEN", "controller-admin-token")
	t.Setenv("DIVERGE_UNLEASH_CLIENT_TOKEN", "controller-client-token")

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	// Tenant Secret supplies only `url` without tokens
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tenant-unleash",
			Namespace: "tenant-ns",
		},
		Data: map[string][]byte{
			"url": []byte(ts.URL),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()
	provider := NewUnleashProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-99",
			Namespace: "tenant-ns",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "unleash",
				ConnectionRef: "tenant-unleash",
			},
		},
	}

	res, err := provider.Provision(context.Background(), env)
	require.NoError(t, err)
	assert.Empty(t, receivedAuth, "controller admin token must not be sent to a Secret-supplied URL")
	assert.Empty(t, res.EnvVars["UNLEASH_API_TOKEN"], "controller client token must not be paired with a Secret-supplied URL")
}

func TestUnleashProvider_Provision_EnvVarFallback(t *testing.T) {
	ts, _ := newMockUnleashServer("env-admin-token")
	defer ts.Close()

	t.Setenv("DIVERGE_UNLEASH_URL", ts.URL)
	t.Setenv("DIVERGE_UNLEASH_ADMIN_TOKEN", "env-admin-token")
	t.Setenv("DIVERGE_UNLEASH_CLIENT_TOKEN", "env-client-token")

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	provider := NewUnleashProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-envvar",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider: "unleash",
			},
		},
	}

	res, err := provider.Provision(context.Background(), env)
	require.NoError(t, err)
	assert.Equal(t, "env-client-token", res.EnvVars["UNLEASH_API_TOKEN"])
}

func TestUnleashProvider_Status_UnhealthyAndErrors(t *testing.T) {
	ts, mock := newMockUnleashServer("test-admin")
	defer ts.Close()
	mock.forceHealthErr = true

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash-sec",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"url":        []byte(ts.URL),
			"adminToken": []byte("test-admin"),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()
	provider := NewUnleashProvider(c, scheme, logr.Discard()).WithHTTPClient(&http.Client{})

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-status",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "unleash",
				ConnectionRef: "unleash-sec",
			},
		},
	}

	status, err := provider.Status(context.Background(), env)
	require.NoError(t, err)
	assert.False(t, status.Ready)
	assert.Contains(t, status.Message, "health check failed")

	// Nil env checks
	_, err = provider.Provision(context.Background(), nil)
	assert.Error(t, err)
	err = provider.Teardown(context.Background(), nil)
	assert.Error(t, err)
	_, err = provider.Status(context.Background(), nil)
	assert.Error(t, err)
}
