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

type mockFlagsmithServer struct {
	mu             sync.Mutex
	identities     map[string]*FlagsmithIdentity
	featureStates  map[string]map[string]FlagsmithFeatureState // identity -> featureName -> state
	reqEnvKey      string
	reqMasterKey   string
	forceHealthErr bool
}

// newMockFlagsmithServer initializes an in-memory HTTP server simulating Flagsmith API endpoints.
func newMockFlagsmithServer(reqEnvKey, reqMasterKey string) (*httptest.Server, *mockFlagsmithServer) {
	mock := &mockFlagsmithServer{
		identities:    make(map[string]*FlagsmithIdentity),
		featureStates: make(map[string]map[string]FlagsmithFeatureState),
		reqEnvKey:     reqEnvKey,
		reqMasterKey:  reqMasterKey,
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		defer mock.mu.Unlock()

		if mock.reqEnvKey != "" {
			if r.Header.Get("X-Environment-Key") != mock.reqEnvKey {
				http.Error(w, `{"error":"unauthorized environment key"}`, http.StatusUnauthorized)
				return
			}
		}

		if mock.reqMasterKey != "" {
			expectedAuth := "Api-Key " + mock.reqMasterKey
			if r.Header.Get("Authorization") != expectedAuth {
				http.Error(w, `{"error":"unauthorized master key"}`, http.StatusUnauthorized)
				return
			}
		}

		path := r.URL.Path

		// Health / Flags check
		if path == "/health" || path == "/api/v1/flags/" || path == "/flags/" {
			if mock.forceHealthErr {
				http.Error(w, `{"status":"down"}`, http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}

		// Ensure Identity with Traits: POST /api/v1/identities/ or /identities/
		if (path == "/api/v1/identities/" || path == "/identities/") && r.Method == http.MethodPost {
			var body struct {
				Identifier string           `json:"identifier"`
				Traits     []FlagsmithTrait `json:"traits"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			id, exists := mock.identities[body.Identifier]
			if !exists {
				id = &FlagsmithIdentity{
					Identifier: body.Identifier,
					Traits:     body.Traits,
				}
				mock.identities[body.Identifier] = id
			} else {
				id.Traits = append(id.Traits, body.Traits...)
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(id)
			return
		}

		// Set Feature State: POST /api/v1/environments/{envKey}/identities/{identifier}/featurestates/
		if strings.Contains(path, "/featurestates/") && r.Method == http.MethodPost {
			parts := strings.Split(strings.Trim(path, "/"), "/")
			// parts can be:
			// ["api", "v1", "environments", envKey, "identities", identifier, "featurestates"]
			// or ["identities", identifier, "featurestates"]
			identifier := ""
			for i, p := range parts {
				if p == "identities" && i+1 < len(parts) {
					identifier = parts[i+1]
					break
				}
			}

			var body struct {
				Feature struct {
					Name string `json:"name"`
				} `json:"feature"`
				Enabled           bool        `json:"enabled"`
				FeatureStateValue interface{} `json:"feature_state_value"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			if mock.featureStates[identifier] == nil {
				mock.featureStates[identifier] = make(map[string]FlagsmithFeatureState)
			}
			state := FlagsmithFeatureState{
				Feature:           FlagsmithFeature{Name: body.Feature.Name},
				Enabled:           body.Enabled,
				FeatureStateValue: body.FeatureStateValue,
			}
			mock.featureStates[identifier][body.Feature.Name] = state

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(state)
			return
		}

		// Delete Identity: DELETE /api/v1/environments/{envKey}/identities/{identifier}/
		if strings.Contains(path, "/identities/") && r.Method == http.MethodDelete {
			parts := strings.Split(strings.Trim(path, "/"), "/")
			identifier := ""
			for i, p := range parts {
				if p == "identities" && i+1 < len(parts) {
					identifier = parts[i+1]
					break
				}
			}
			if identifier == "" {
				identifier = r.URL.Query().Get("identifier")
			}

			if _, exists := mock.identities[identifier]; exists {
				delete(mock.identities, identifier)
				delete(mock.featureStates, identifier)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Error(w, `{"error":"identity not found"}`, http.StatusNotFound)
			return
		}

		http.Error(w, fmt.Sprintf("unhandled path: %s", path), http.StatusNotFound)
	}))

	return ts, mock
}

// TestFlagsmithProvider_Type verifies that FlagsmithProvider returns "flagsmith".
func TestFlagsmithProvider_Type(t *testing.T) {
	provider := NewFlagsmithProvider(nil, nil, logr.Discard())
	assert.Equal(t, "flagsmith", provider.Type())
}

// TestFlagsmithIdentityName verifies RFC 1123 compliant naming and length capping.
func TestFlagsmithIdentityName(t *testing.T) {
	assert.Contains(t, FlagsmithIdentityName("default", "my-preview"), "diverge-my-preview-")
	assert.Contains(t, FlagsmithIdentityName("tenant-a", "feature-1"), "diverge-tenant-a-feature-1-")

	// Distinct namespaces produce distinct identities even with same env name
	id1 := FlagsmithIdentityName("ns1", "my-env")
	id2 := FlagsmithIdentityName("ns2", "my-env")
	assert.NotEqual(t, id1, id2)

	// Length bounds check
	longName := strings.Repeat("a", 100)
	id := FlagsmithIdentityName("long-namespace", longName)
	assert.True(t, strings.HasPrefix(id, "diverge-"))
	assert.LessOrEqual(t, len(id), 63)
}

// TestFlagsmithClient_Operations verifies end-to-end client calls for health checks, identity creation, flag overrides, and teardown.
func TestFlagsmithClient_Operations(t *testing.T) {
	ts, mock := newMockFlagsmithServer("test-env-key", "test-master-key")
	defer ts.Close()

	client, err := NewFlagsmithClient(ts.URL, "test-env-key", "test-master-key", nil)
	require.NoError(t, err)
	require.NotNil(t, client)

	ctx := context.Background()

	// 1. HealthCheck
	require.NoError(t, client.HealthCheck(ctx))

	// 2. EnsureIdentityWithTraits
	traits := map[string]interface{}{
		"diverge_environment": "test-env",
		"diverge_preview":     true,
	}
	identity, err := client.EnsureIdentityWithTraits(ctx, "diverge-test-env", traits)
	require.NoError(t, err)
	assert.Equal(t, "diverge-test-env", identity.Identifier)
	assert.Len(t, identity.Traits, 2)

	// 3. SetIdentityFeatureState
	state, err := client.SetIdentityFeatureState(ctx, "diverge-test-env", "dark_mode", true, "true")
	require.NoError(t, err)
	assert.True(t, state.Enabled)
	assert.Equal(t, "dark_mode", state.Feature.Name)

	mock.mu.Lock()
	savedState, stateExists := mock.featureStates["diverge-test-env"]["dark_mode"]
	mock.mu.Unlock()
	require.True(t, stateExists)
	assert.True(t, savedState.Enabled)

	// 4. DeleteIdentity
	err = client.DeleteIdentity(ctx, "diverge-test-env")
	require.NoError(t, err)

	mock.mu.Lock()
	_, identityExists := mock.identities["diverge-test-env"]
	mock.mu.Unlock()
	assert.False(t, identityExists)
}

// TestFlagsmithProvider_Provision_Tier1Secret tests provisioning using a Secret in the environment's own namespace.
func TestFlagsmithProvider_Provision_Tier1Secret(t *testing.T) {
	ts, mock := newMockFlagsmithServer("tier1-key", "tier1-admin")
	defer ts.Close()

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-flagsmith-secret",
			Namespace: "app-ns",
		},
		Data: map[string][]byte{
			"url":            []byte(ts.URL),
			"environmentKey": []byte("tier1-key"),
			"masterApiKey":   []byte("tier1-admin"),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()
	provider := NewFlagsmithProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-42",
			Namespace: "app-ns",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "flagsmith",
				ConnectionRef: "my-flagsmith-secret",
				Overrides: map[string]string{
					"checkout_v2": "true",
					"dark_mode":   "false",
					"max_retries": "5",
					"rate_limit":  "12.5",
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
	expectedIdentity := FlagsmithIdentityName(env.Namespace, env.Name)
	assert.Equal(t, "flagsmith", res.ProviderType)
	assert.Equal(t, "tier1-key", res.EnvVars["FLAGSMITH_ENVIRONMENT_KEY"])
	assert.Equal(t, ts.URL, res.EnvVars["FLAGSMITH_API_URL"])
	assert.Equal(t, expectedIdentity, res.EnvVars["FLAGSMITH_IDENTITY"])
	assert.Equal(t, expectedIdentity, res.EnvVars["OPENFEATURE_TARGET_KEY"])

	// Verify traits in mock server
	mock.mu.Lock()
	identity, ok := mock.identities[expectedIdentity]
	mock.mu.Unlock()
	require.True(t, ok)
	require.NotNil(t, identity)

	traits := make(map[string]interface{})
	for _, tr := range identity.Traits {
		traits[tr.TraitKey] = tr.TraitValue
	}
	assert.Equal(t, "pr-42", traits["diverge_environment"])
	assert.Equal(t, "app-ns", traits["diverge_namespace"])
	assert.Equal(t, true, traits["diverge_preview"])

	// Verify flag overrides in mock server
	mock.mu.Lock()
	states := mock.featureStates[expectedIdentity]
	mock.mu.Unlock()
	require.NotNil(t, states)

	assert.True(t, states["checkout_v2"].Enabled)
	assert.Equal(t, "true", states["checkout_v2"].FeatureStateValue)

	assert.False(t, states["dark_mode"].Enabled)
	assert.Equal(t, "false", states["dark_mode"].FeatureStateValue)

	assert.True(t, states["max_retries"].Enabled)
	assert.EqualValues(t, 5, states["max_retries"].FeatureStateValue)

	assert.True(t, states["rate_limit"].Enabled)
	assert.Equal(t, 12.5, states["rate_limit"].FeatureStateValue)

	assert.True(t, states["theme"].Enabled)
	assert.Equal(t, "solarized", states["theme"].FeatureStateValue)

	// Status check
	status, err := provider.Status(ctx, env)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.True(t, status.Ready)

	// Teardown
	err = provider.Teardown(ctx, env)
	require.NoError(t, err)

	mock.mu.Lock()
	_, stillExists := mock.identities[expectedIdentity]
	mock.mu.Unlock()
	assert.False(t, stillExists)
}

// TestFlagsmithProvider_Provision_Tier2Fallback tests secret fallback to the diverge-system namespace.
func TestFlagsmithProvider_Provision_Tier2Fallback(t *testing.T) {
	ts, _ := newMockFlagsmithServer("tier2-key", "")
	defer ts.Close()

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	// Secret only exists in diverge-system namespace
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shared-flagsmith",
			Namespace: "diverge-system",
		},
		Data: map[string][]byte{
			"FLAGSMITH_URL":             []byte(ts.URL),
			"FLAGSMITH_ENVIRONMENT_KEY": []byte("tier2-key"),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()
	provider := NewFlagsmithProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "feat-x",
			Namespace: "tenant-ns",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "flagsmith",
				ConnectionRef: "shared-flagsmith",
			},
		},
	}

	res, err := provider.Provision(context.Background(), env)
	require.NoError(t, err)
	assert.Equal(t, "tier2-key", res.EnvVars["FLAGSMITH_ENVIRONMENT_KEY"])
	assert.Equal(t, FlagsmithIdentityName(env.Namespace, env.Name), res.EnvVars["FLAGSMITH_IDENTITY"])
}

// TestFlagsmithProvider_Provision_DefaultSecrets tests default secret name discovery when connectionRef is omitted.
func TestFlagsmithProvider_Provision_DefaultSecrets(t *testing.T) {
	ts, _ := newMockFlagsmithServer("default-secret-key", "")
	defer ts.Close()

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flagsmith-connection",
			Namespace: "app-ns",
		},
		Data: map[string][]byte{
			"url":            []byte(ts.URL),
			"environmentKey": []byte("default-secret-key"),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()
	provider := NewFlagsmithProvider(c, scheme, logr.Discard())

	// ConnectionRef is empty, should automatically find flagsmith-connection
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-default-sec",
			Namespace: "app-ns",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider: "flagsmith",
			},
		},
	}

	res, err := provider.Provision(context.Background(), env)
	require.NoError(t, err)
	assert.Equal(t, "default-secret-key", res.EnvVars["FLAGSMITH_ENVIRONMENT_KEY"])
}

// TestFlagsmithProvider_Provision_EnvVarFallback tests environment variable resolution fallback.
func TestFlagsmithProvider_Provision_EnvVarFallback(t *testing.T) {
	ts, _ := newMockFlagsmithServer("envvar-key", "")
	defer ts.Close()

	t.Setenv("DIVERGE_FLAGSMITH_URL", ts.URL)
	t.Setenv("DIVERGE_FLAGSMITH_ENVIRONMENT_KEY", "envvar-key")

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	provider := NewFlagsmithProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-envvar",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider: "flagsmith",
			},
		},
	}

	res, err := provider.Provision(context.Background(), env)
	require.NoError(t, err)
	assert.Equal(t, "envvar-key", res.EnvVars["FLAGSMITH_ENVIRONMENT_KEY"])
	assert.Equal(t, FlagsmithIdentityName(env.Namespace, env.Name), res.EnvVars["FLAGSMITH_IDENTITY"])
}

// TestFlagsmithProvider_SSRFRejection tests rejection of cloud metadata IP addresses.
func TestFlagsmithProvider_SSRFRejection(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	// Blocked metadata IP
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bad-sec",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"url":            []byte("http://169.254.169.254/api/v1"),
			"environmentKey": []byte("key"),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()
	provider := NewFlagsmithProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ssrf",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "flagsmith",
				ConnectionRef: "bad-sec",
			},
		},
	}

	_, err := provider.Provision(context.Background(), env)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "prohibited")
}

// TestFlagsmithProvider_Status_Unhealthy tests reporting unhealthy status when Flagsmith endpoint fails health check.
func TestFlagsmithProvider_Status_Unhealthy(t *testing.T) {
	ts, mock := newMockFlagsmithServer("healthy-key", "")
	defer ts.Close()

	mock.forceHealthErr = true

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flagsmith-sec",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"url":            []byte(ts.URL),
			"environmentKey": []byte("healthy-key"),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()
	provider := NewFlagsmithProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-status",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "flagsmith",
				ConnectionRef: "flagsmith-sec",
			},
		},
	}

	status, err := provider.Status(context.Background(), env)
	require.NoError(t, err)
	assert.False(t, status.Ready)
	assert.Contains(t, status.Message, "health check failed")
}

// TestFlagsmithProvider_WithHTTPClient tests custom HTTP client setter.
func TestFlagsmithProvider_WithHTTPClient(t *testing.T) {
	customClient := &http.Client{}
	provider := NewFlagsmithProvider(nil, nil, logr.Discard()).WithHTTPClient(customClient)
	assert.Equal(t, customClient, provider.httpClient)
}

// TestFlagsmithClient_ValidationErrors tests client validation error scenarios.
func TestFlagsmithClient_ValidationErrors(t *testing.T) {
	// Invalid scheme
	_, err := NewFlagsmithClient("ftp://api.flagsmith.com", "key", "", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scheme")

	// Empty host
	_, err = NewFlagsmithClient("http:///path", "key", "", nil)
	assert.Error(t, err)

	// Prohibited hosts
	for _, host := range []string{"http://metadata.google.internal", "http://metadata", "http://instance-data"} {
		_, err := NewFlagsmithClient(host, "key", "", nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "prohibited")
	}

	// Prohibited IP
	_, err = NewFlagsmithClient("http://169.254.169.254/api/v1", "key", "", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "prohibited")

	// Valid default
	c, err := NewFlagsmithClient("", "key", "", nil)
	require.NoError(t, err)
	assert.Equal(t, "https://edge.api.flagsmith.com/api/v1", c.baseURL)

	ctx := context.Background()
	// Empty identifier
	_, err = c.EnsureIdentityWithTraits(ctx, "", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "identifier cannot be empty")

	_, err = c.SetIdentityFeatureState(ctx, "", "feature", true, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "identifier cannot be empty")

	_, err = c.SetIdentityFeatureState(ctx, "id", "", true, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "featureName cannot be empty")

	err = c.DeleteIdentity(ctx, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "identifier cannot be empty")
}

// TestFlagsmithClient_ErrorResponses tests handling of 401 Unauthorized and 500 Internal Server Error.
func TestFlagsmithClient_ErrorResponses(t *testing.T) {
	var statusCode int
	var respBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(respBody))
	}))
	defer srv.Close()

	c, err := NewFlagsmithClient(srv.URL, "k", "m", nil)
	require.NoError(t, err)
	ctx := context.Background()

	// 401 Unauthorized
	statusCode = http.StatusUnauthorized
	respBody = `{"detail":"Invalid API Key"}`
	err = c.HealthCheck(ctx)
	assert.ErrorIs(t, err, ErrFlagsmithUnauthorized)

	// 500 Internal Error
	statusCode = http.StatusInternalServerError
	respBody = `{"detail":"Server error"}`
	_, err = c.EnsureIdentityWithTraits(ctx, "test-id", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}

// TestFlagsmithProvider_NilEnv tests nil environment handling across provider lifecycle methods.
func TestFlagsmithProvider_NilEnv(t *testing.T) {
	provider := NewFlagsmithProvider(nil, nil, logr.Discard())
	ctx := context.Background()

	_, err := provider.Provision(ctx, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "environment cannot be nil")

	err = provider.Teardown(ctx, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "environment cannot be nil")

	_, err = provider.Status(ctx, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "environment cannot be nil")
}

// TestFlagsmithProvider_SecretNotFound tests error behavior when referenced secret is absent.
func TestFlagsmithProvider_SecretNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	provider := NewFlagsmithProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "flagsmith",
				ConnectionRef: "non-existent-secret",
			},
		},
	}

	_, err := provider.Provision(context.Background(), env)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestFlagsmithProvider_Teardown_ErrorPropagation tests that teardown propagates errors from connection resolution or identity deletion.
func TestFlagsmithProvider_Teardown_ErrorPropagation(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	provider := NewFlagsmithProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "flagsmith",
				ConnectionRef: "non-existent-secret",
			},
		},
	}

	err := provider.Teardown(context.Background(), env)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resolve flagsmith connection for teardown")

	// Server returning 500 error on DELETE
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flagsmith-sec",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"url":            []byte(srv.URL),
			"environmentKey": []byte("env-key"),
		},
	}

	c2 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()
	provider2 := NewFlagsmithProvider(c2, scheme, logr.Discard())
	env2 := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-fail",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "flagsmith",
				ConnectionRef: "flagsmith-sec",
			},
		},
	}

	err2 := provider2.Teardown(context.Background(), env2)
	assert.Error(t, err2)
	assert.Contains(t, err2.Error(), "failed to delete flagsmith identity")
}
