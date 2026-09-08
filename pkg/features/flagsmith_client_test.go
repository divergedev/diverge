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

// TestNewFlagsmithClient_DirectValidation tests URL validation, scheme validation, and client configuration options.
func TestNewFlagsmithClient_DirectValidation(t *testing.T) {
	// Invalid scheme
	_, err := NewFlagsmithClient("ftp://flagsmith.example.com", "", "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be http or https")

	// Empty host
	_, err = NewFlagsmithClient("http://", "", "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host cannot be empty")

	// Custom client
	custom := &http.Client{Timeout: 5 * time.Second}
	fc, err := NewFlagsmithClient("https://flagsmith.example.com/api/v1/", "env-key", "master-key", custom)
	require.NoError(t, err)
	assert.Equal(t, "https://flagsmith.example.com/api/v1", fc.baseURL)
	assert.Equal(t, "env-key", fc.environmentKey)
	assert.Equal(t, "master-key", fc.masterAPIKey)
	assert.Equal(t, custom, fc.httpClient)

	// Default client timeout
	fcDefault, err := NewFlagsmithClient("http://flagsmith:8000", "", "", nil)
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, fcDefault.httpClient.Timeout)
}

// TestFlagsmithClient_Methods verifies FlagsmithClient HTTP request generation, headers, and response parsing.
func TestFlagsmithClient_Methods(t *testing.T) {
	var lastMethod, lastPath, lastEnvKey, lastAuth string
	var lastBody map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod = r.Method
		lastPath = r.URL.Path
		lastEnvKey = r.Header.Get("X-Environment-Key")
		lastAuth = r.Header.Get("Authorization")

		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&lastBody)
		}

		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/identities/" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"identifier":"test-id","traits":[{"trait_key":"k","trait_value":"v"}]}`))
		case r.URL.Path == "/api/v1/environments/my-env/identities/test-id/featurestates/" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"feature":{"name":"flag_1"},"enabled":true,"feature_state_value":"v1"}`))
		case r.URL.Path == "/api/v1/environments/my-env/identities/test-id/" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/v1/flags/" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	ctx := context.Background()
	client, err := NewFlagsmithClient(ts.URL, "my-env", "my-master", nil)
	require.NoError(t, err)

	// 1. EnsureIdentityWithTraits
	traits := map[string]interface{}{"k": "v"}
	id, err := client.EnsureIdentityWithTraits(ctx, "test-id", traits)
	require.NoError(t, err)
	assert.Equal(t, "test-id", id.Identifier)
	assert.Equal(t, http.MethodPost, lastMethod)
	assert.Equal(t, "/api/v1/identities/", lastPath)
	assert.Equal(t, "my-env", lastEnvKey)
	assert.Equal(t, "Api-Key my-master", lastAuth)

	// 2. SetIdentityFeatureState
	fs, err := client.SetIdentityFeatureState(ctx, "test-id", "flag_1", true, "v1")
	require.NoError(t, err)
	assert.Equal(t, "flag_1", fs.Feature.Name)
	assert.True(t, fs.Enabled)
	assert.Equal(t, http.MethodPost, lastMethod)
	assert.Equal(t, "/api/v1/environments/my-env/identities/test-id/featurestates/", lastPath)

	// 3. DeleteIdentity
	err = client.DeleteIdentity(ctx, "test-id")
	require.NoError(t, err)
	assert.Equal(t, http.MethodDelete, lastMethod)
	assert.Equal(t, "/api/v1/environments/my-env/identities/test-id/", lastPath)

	// 4. HealthCheck
	err = client.HealthCheck(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, lastMethod)
	assert.Equal(t, "/api/v1/flags/", lastPath)
}
