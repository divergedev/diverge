package features

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnleashClient_HealthCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/health", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"health": "GOOD"}`))
	}))
	defer server.Close()

	client, err := NewUnleashClient(server.URL, "test-token", nil)
	require.NoError(t, err)

	err = client.HealthCheck(context.Background())
	assert.NoError(t, err)
}

func TestUnleashClient_HealthCheck_Unhealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client, err := NewUnleashClient(server.URL, "test-token", nil)
	require.NoError(t, err)

	err = client.HealthCheck(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unleash health check failed (status 500)")
}

func TestUnleashClient_CreateEnvironment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/admin/environments", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client, err := NewUnleashClient(server.URL, "test-token", nil)
	require.NoError(t, err)

	err = client.CreateEnvironment(context.Background(), "diverge-test", "test")
	assert.NoError(t, err)
}

func TestUnleashClient_CreateEnvironment_Conflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()

	client, err := NewUnleashClient(server.URL, "test-token", nil)
	require.NoError(t, err)

	err = client.CreateEnvironment(context.Background(), "diverge-test", "test")
	assert.NoError(t, err)
}

func TestUnleashClient_GetEnvironment_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewUnleashClient(server.URL, "test-token", nil)
	require.NoError(t, err)

	err = client.GetEnvironment(context.Background(), "diverge-test")
	assert.ErrorIs(t, err, ErrUnleashNotFound)
}

func TestUnleashClient_DeleteEnvironment_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewUnleashClient(server.URL, "test-token", nil)
	require.NoError(t, err)

	err = client.DeleteEnvironment(context.Background(), "diverge-test")
	assert.NoError(t, err)
}

func TestUnleashClient_EnableFeature(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/admin/projects/default/features/my-feature/environments/diverge-test/on", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewUnleashClient(server.URL, "test-token", nil)
	require.NoError(t, err)

	err = client.EnableFeature(context.Background(), "default", "my-feature", "diverge-test", true)
	assert.NoError(t, err)
}
