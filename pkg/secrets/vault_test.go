package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVaultResolver_KVv2(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-token", r.Header.Get("X-Vault-Token"))
		assert.Equal(t, "/v1/secret/data/myapp", r.URL.Path)

		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"data": map[string]interface{}{
					"mykey": "v2-secret-value",
				},
			},
		}
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	t.Setenv("VAULT_ADDR", srv.URL)
	t.Setenv("VAULT_TOKEN", "test-token")

	r := NewVaultResolver()
	r.client = srv.Client()
	val, err := r.Resolve(context.Background(), SecretRef{Path: "secret/data/myapp", Key: "mykey"})
	require.NoError(t, err)
	assert.Equal(t, "v2-secret-value", val)
}

func TestVaultResolver_KVv1(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"mykey": "v1-secret-value",
			},
		}
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	t.Setenv("VAULT_ADDR", srv.URL)
	t.Setenv("VAULT_TOKEN", "test-token")

	r := NewVaultResolver()
	r.client = srv.Client()
	val, err := r.Resolve(context.Background(), SecretRef{Path: "secret/myapp", Key: "mykey"})
	require.NoError(t, err)
	assert.Equal(t, "v1-secret-value", val)
}

func TestVaultResolver_KeyNotFound(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"otherkey": "val",
			},
		}
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	t.Setenv("VAULT_ADDR", srv.URL)
	t.Setenv("VAULT_TOKEN", "test-token")

	r := NewVaultResolver()
	r.client = srv.Client()
	_, err := r.Resolve(context.Background(), SecretRef{Path: "secret/myapp", Key: "mykey"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in vault secret")
}

func TestVaultResolver_RejectsHTTP(t *testing.T) {
	t.Setenv("VAULT_ADDR", "http://insecure-vault:8200")
	t.Setenv("VAULT_TOKEN", "test-token")

	r := NewVaultResolver()
	_, err := r.Resolve(context.Background(), SecretRef{Path: "secret/data/myapp", Key: "mykey"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must use HTTPS")
}
