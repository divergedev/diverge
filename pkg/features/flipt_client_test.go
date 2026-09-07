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

func TestNewFliptClient_Validation(t *testing.T) {
	// Invalid scheme
	_, err := NewFliptClient("ftp://flipt.example.com", "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be http or https")

	// Empty host
	_, err = NewFliptClient("http://", "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host cannot be empty")

	// Custom client
	custom := &http.Client{Timeout: 5 * time.Second}
	fc, err := NewFliptClient("https://flipt.example.com/", "my-token", custom)
	require.NoError(t, err)
	assert.Equal(t, "https://flipt.example.com", fc.baseURL)
	assert.Equal(t, "my-token", fc.token)
	assert.Equal(t, custom, fc.httpClient)

	// Default client timeout
	fcDefault, err := NewFliptClient("http://flipt:8080", "", nil)
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, fcDefault.httpClient.Timeout)
}

func TestFliptClient_Methods(t *testing.T) {
	var lastMethod, lastPath, lastAuth string
	var lastBody map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod = r.Method
		lastPath = r.URL.Path
		lastAuth = r.Header.Get("Authorization")

		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&lastBody)
		}

		switch {
		case r.URL.Path == "/api/v1/namespaces" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "created"})
		case r.URL.Path == "/api/v1/namespaces/exists" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"key": "exists"})
		case r.URL.Path == "/api/v1/namespaces/missing" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/api/v1/namespaces/to-delete" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/v1/namespaces/diverge-test/flags" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	ctx := context.Background()
	client, err := NewFliptClient(ts.URL, "auth-123", nil)
	require.NoError(t, err)

	// 1. CreateNamespace
	err = client.CreateNamespace(ctx, "diverge-test", "Diverge Test", "Description")
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, lastMethod)
	assert.Equal(t, "/api/v1/namespaces", lastPath)
	assert.Equal(t, "Bearer auth-123", lastAuth)
	assert.Equal(t, "diverge-test", lastBody["key"])

	// 2. GetNamespace exists
	err = client.GetNamespace(ctx, "exists")
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, lastMethod)
	assert.Equal(t, "/api/v1/namespaces/exists", lastPath)

	// 3. GetNamespace missing -> ErrFliptNotFound
	err = client.GetNamespace(ctx, "missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFliptNotFound)

	// 4. DeleteNamespace
	err = client.DeleteNamespace(ctx, "to-delete")
	require.NoError(t, err)
	assert.Equal(t, http.MethodDelete, lastMethod)
	assert.Equal(t, "/api/v1/namespaces/to-delete", lastPath)

	// 5. CreateOrUpdateFlag
	err = client.CreateOrUpdateFlag(ctx, "diverge-test", "my_bool", "BOOLEAN_FLAG_TYPE", true, "")
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, lastMethod)
	assert.Equal(t, "/api/v1/namespaces/diverge-test/flags", lastPath)
	assert.Equal(t, "my_bool", lastBody["key"])
}
