package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
				"metadata": map[string]interface{}{},
			},
		}
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	t.Setenv("BAO_ADDR", "")
	t.Setenv("BAO_TOKEN", "")
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

	t.Setenv("BAO_ADDR", "")
	t.Setenv("BAO_TOKEN", "")
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

	t.Setenv("BAO_ADDR", "")
	t.Setenv("BAO_TOKEN", "")
	t.Setenv("VAULT_ADDR", srv.URL)
	t.Setenv("VAULT_TOKEN", "test-token")

	r := NewVaultResolver()
	r.client = srv.Client()
	_, err := r.Resolve(context.Background(), SecretRef{Path: "secret/myapp", Key: "mykey"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in vault secret")
}

func TestVaultResolver_RejectsHTTP(t *testing.T) {
	t.Setenv("BAO_ADDR", "")
	t.Setenv("BAO_TOKEN", "")
	t.Setenv("VAULT_ADDR", "http://insecure-vault:8200")
	t.Setenv("VAULT_TOKEN", "test-token")

	r := NewVaultResolver()
	_, err := r.Resolve(context.Background(), SecretRef{Path: "secret/data/myapp", Key: "mykey"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must use HTTPS")
}

func TestVaultResolver_RejectsCrossHostRedirect(t *testing.T) {
	// Create the target server (different host)
	targetSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer targetSrv.Close()

	// Create the initial server that redirects to the target server
	redirectSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetSrv.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirectSrv.Close()

	t.Setenv("BAO_ADDR", "")
	t.Setenv("BAO_TOKEN", "")
	t.Setenv("VAULT_ADDR", redirectSrv.URL)
	t.Setenv("VAULT_TOKEN", "test-token")

	r := NewVaultResolver()
	// Need to use a custom transport that trusts the test certificates
	tr := redirectSrv.Client().Transport.(*http.Transport).Clone()
	// Add trust for the target server's certificate too
	tr.TLSClientConfig.RootCAs = nil
	tr.TLSClientConfig.InsecureSkipVerify = true
	r.client.Transport = tr

	_, err := r.Resolve(context.Background(), SecretRef{Path: "secret/data/myapp", Key: "mykey"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing cross-host redirect from")
}

func TestVaultResolver_SetRole(t *testing.T) {
	r := NewVaultResolver()
	r.SetRole("my-role", "custom-mount")
	assert.Equal(t, "my-role", r.role)
	assert.Equal(t, "custom-mount", r.mountPath)
}

func TestVaultResolver_GetToken(t *testing.T) {
	t.Run("VAULT_TOKEN set", func(t *testing.T) {
		t.Setenv("BAO_TOKEN", "")
		t.Setenv("VAULT_TOKEN", "test-token")
		r := NewVaultResolver()
		token, err := r.getToken(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "test-token", token)
	})

	t.Run("No VAULT_TOKEN and no SA token", func(t *testing.T) {
		t.Setenv("VAULT_TOKEN", "")
		t.Setenv("BAO_TOKEN", "")
		t.Setenv("HOME", t.TempDir())
		r := NewVaultResolver()
		_, err := r.getToken(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no BAO_TOKEN or VAULT_TOKEN set and kubernetes token not found")
	})

	t.Run("No VAULT_TOKEN, role set, no SA token", func(t *testing.T) {
		t.Setenv("VAULT_TOKEN", "")
		t.Setenv("BAO_TOKEN", "")
		t.Setenv("HOME", t.TempDir())
		r := NewVaultResolver()
		r.SetRole("my-role", "")
		_, err := r.getToken(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no BAO_TOKEN or VAULT_TOKEN set and kubernetes token not found")
	})

	t.Run("BAO_TOKEN takes precedence over VAULT_TOKEN", func(t *testing.T) {
		t.Setenv("BAO_TOKEN", "bao-precedence-token")
		t.Setenv("VAULT_TOKEN", "vault-fallback-token")
		r := NewVaultResolver()
		token, err := r.getToken(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "bao-precedence-token", token)
	})

	t.Run("reads from ~/.bao-token if env unset", func(t *testing.T) {
		t.Setenv("BAO_TOKEN", "")
		t.Setenv("VAULT_TOKEN", "")
		tempHome := t.TempDir()
		t.Setenv("HOME", tempHome)
		err := os.WriteFile(tempHome+"/.bao-token", []byte("file-bao-token\n"), 0o600)
		require.NoError(t, err)

		r := NewVaultResolver()
		token, err := r.getToken(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "file-bao-token", token)
	})

	t.Run("reads from ~/.vault-token if ~/.bao-token absent and env unset", func(t *testing.T) {
		t.Setenv("BAO_TOKEN", "")
		t.Setenv("VAULT_TOKEN", "")
		tempHome := t.TempDir()
		t.Setenv("HOME", tempHome)
		err := os.WriteFile(tempHome+"/.vault-token", []byte("file-vault-token\n"), 0o600)
		require.NoError(t, err)

		r := NewVaultResolver()
		token, err := r.getToken(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "file-vault-token", token)
	})
}

func TestOpenBao_Interoperability(t *testing.T) {
	var gotVaultToken, gotBaoToken string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotVaultToken = r.Header.Get("X-Vault-Token")
		gotBaoToken = r.Header.Get("X-Bao-Token")
		assert.Equal(t, "/v1/secret/data/myapp", r.URL.Path)

		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"data": map[string]interface{}{
					"mykey": "bao-secret-value",
				},
				"metadata": map[string]interface{}{},
			},
		}
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	t.Setenv("BAO_ADDR", srv.URL)
	t.Setenv("VAULT_ADDR", "https://other-vault:8200")
	t.Setenv("BAO_TOKEN", "bao-secret-token")
	t.Setenv("VAULT_TOKEN", "vault-secret-token")

	r := NewOpenBaoResolver()
	assert.Equal(t, srv.URL, r.addr, "BAO_ADDR must take precedence over VAULT_ADDR")
	assert.Equal(t, "bao-secret-token", r.token, "BAO_TOKEN must take precedence over VAULT_TOKEN")

	r.client = srv.Client()
	val, err := r.Resolve(context.Background(), SecretRef{Path: "secret/data/myapp", Key: "mykey"})
	require.NoError(t, err)
	assert.Equal(t, "bao-secret-value", val)
	assert.Equal(t, "bao-secret-token", gotVaultToken, "X-Vault-Token header must be set")
	assert.Equal(t, "bao-secret-token", gotBaoToken, "X-Bao-Token header must be set")
}

func TestVaultResolver_MaxRedirectLimit(t *testing.T) {
	redirectCount := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectCount++
		if redirectCount > 12 {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()

	t.Setenv("VAULT_ADDR", srv.URL)
	t.Setenv("VAULT_TOKEN", "test-token")

	r := NewVaultResolver()
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.RootCAs = nil
	tr.TLSClientConfig.InsecureSkipVerify = true
	r.client.Transport = tr

	_, err := r.Resolve(context.Background(), SecretRef{Path: "secret/data/myapp", Key: "mykey"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopped after 10 redirects")
}
