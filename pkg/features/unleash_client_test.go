package features

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewUnleashClient_DirectValidation tests URL validation, scheme validation, and client defaults.
func TestNewUnleashClient_DirectValidation(t *testing.T) {
	// Empty baseURL fails
	_, err := NewUnleashClient("", "", "", "test-admin", "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unleash url cannot be empty")

	// Invalid scheme
	_, err = NewUnleashClient("ftp://unleash.example.com", "", "", "test-admin", "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be http or https")

	// Empty host
	_, err = NewUnleashClient("http://", "", "", "test-admin", "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host cannot be empty")

	// Prohibited SSRF hosts
	for _, host := range []string{"http://metadata.google.internal", "http://metadata", "http://instance-data", "http://169.254.169.254"} {
		_, err := NewUnleashClient(host, "", "", "test-admin", "", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "prohibited")
	}

	// Custom HTTP client and trailing slash normalization
	custom := &http.Client{Timeout: 5 * time.Second}
	uc, err := NewUnleashClient("https://unleash.example.com/api/", "my-project", "staging", "test-admin", "test-client", custom)
	require.NoError(t, err)
	assert.Equal(t, "https://unleash.example.com/api", uc.baseURL)
	assert.Equal(t, "/admin/projects/my-project", uc.resolvePath("/api/admin/projects/my-project"))
	assert.Equal(t, "my-project", uc.projectID)
	assert.Equal(t, "staging", uc.environment)
	assert.Equal(t, "test-admin", uc.adminToken)
	assert.Equal(t, "test-client", uc.clientToken)
	assert.Equal(t, custom.Timeout, uc.httpClient.Timeout)
	assert.NotNil(t, uc.httpClient.CheckRedirect)

	// Default project and environment
	ucDefault, err := NewUnleashClient("http://unleash:4242", "", "", "test-admin", "", nil)
	require.NoError(t, err)
	assert.Equal(t, "default", ucDefault.projectID)
	assert.Equal(t, "development", ucDefault.environment)
	assert.Equal(t, 10*time.Second, ucDefault.httpClient.Timeout)
}

// TestUnleashClient_CrossOriginRedirect verifies that Authorization headers are stripped on cross-origin redirects.
func TestUnleashClient_CrossOriginRedirect(t *testing.T) {
	var targetReceivedAuth string
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetReceivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"health":"GOOD"}`))
	}))
	defer targetServer.Close()

	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetServer.URL+"/internal-backstage/health", http.StatusFound)
	}))
	defer redirectServer.Close()

	uc, err := NewUnleashClient(redirectServer.URL, "default", "development", "test-admin", "", nil)
	require.NoError(t, err)

	err = uc.HealthCheck(context.Background())
	require.NoError(t, err)
	assert.Empty(t, targetReceivedAuth)
}

// TestUnleashClient_Methods verifies UnleashClient request paths, payloads, and responses.
func TestUnleashClient_Methods(t *testing.T) {
	var lastMethod, lastPath, lastAuth string
	var lastBody map[string]interface{}
	featureExists := false
	strategiesExist := false

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod = r.Method
		lastPath = r.URL.Path
		lastAuth = r.Header.Get("Authorization")
		lastBody = nil
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&lastBody)
		}

		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/admin/projects/default/features/dark_mode" && r.Method == http.MethodGet:
			if !featureExists {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"not found"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"dark_mode","type":"release"}`))

		case r.URL.Path == "/api/admin/projects/default/features" && r.Method == http.MethodPost:
			featureExists = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"dark_mode","type":"release"}`))

		case r.URL.Path == "/api/admin/projects/default/features/dark_mode/environments/development/strategies" && r.Method == http.MethodGet:
			if !strategiesExist {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[]`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":"strat-1","name":"flexibleRollout","title":"diverge-pr-1"}]`))

		case r.URL.Path == "/api/admin/projects/default/features/dark_mode/environments/development/strategies" && r.Method == http.MethodPost:
			strategiesExist = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"strat-1","name":"flexibleRollout","title":"diverge-pr-1"}`))

		case r.URL.Path == "/api/admin/projects/default/features/dark_mode/environments/development/strategies/strat-1" && r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"strat-1","name":"flexibleRollout","title":"diverge-pr-1"}`))

		case r.URL.Path == "/api/admin/projects/default/features" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"features":[{"name":"dark_mode","type":"release"}]}`))

		case r.URL.Path == "/api/admin/projects/default/features/dark_mode/environments/development/strategies/strat-1" && r.Method == http.MethodDelete:
			strategiesExist = false
			w.WriteHeader(http.StatusOK)

		case r.URL.Path == "/internal-backstage/health" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"health":"GOOD"}`))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	ctx := context.Background()
	uc, err := NewUnleashClient(ts.URL, "default", "development", "test-admin", "", nil)
	require.NoError(t, err)

	// 1. EnsureFeature creates when 404
	feat, err := uc.EnsureFeature(ctx, "dark_mode")
	require.NoError(t, err)
	assert.Equal(t, "dark_mode", feat.Name)
	assert.Equal(t, http.MethodPost, lastMethod)
	assert.Equal(t, "/api/admin/projects/default/features", lastPath)
	assert.Equal(t, "test-admin", lastAuth)

	// 2. EnsureFeature returns existing when 200
	feat, err = uc.EnsureFeature(ctx, "dark_mode")
	require.NoError(t, err)
	assert.Equal(t, "dark_mode", feat.Name)
	assert.Equal(t, http.MethodGet, lastMethod)

	// 3. UpsertPreviewStrategy creates strategy when not present
	strat, err := uc.UpsertPreviewStrategy(ctx, "dark_mode", "diverge-pr-1", "pr-1", true, "")
	require.NoError(t, err)
	assert.Equal(t, "strat-1", strat.ID)
	assert.Equal(t, http.MethodPost, lastMethod)

	// 4. UpsertPreviewStrategy updates existing strategy when title matches
	strat, err = uc.UpsertPreviewStrategy(ctx, "dark_mode", "diverge-pr-1", "pr-1", true, "variant-b")
	require.NoError(t, err)
	assert.Equal(t, "strat-1", strat.ID)
	assert.Equal(t, http.MethodPut, lastMethod)

	// 5. DeletePreviewStrategies deletes matching strategy across project features
	err = uc.DeletePreviewStrategies(ctx, "diverge-pr-1", nil)
	require.NoError(t, err)
	assert.Equal(t, http.MethodDelete, lastMethod)

	// 6. HealthCheck
	err = uc.HealthCheck(ctx)
	require.NoError(t, err)
}
