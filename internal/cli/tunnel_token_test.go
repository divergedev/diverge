package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	"pgregory.net/rapid"
)

func TestStaticTokenSource(t *testing.T) {
	t.Run("valid token", func(t *testing.T) {
		ts := StaticTokenSource("my-token")
		tok, err := ts.Token(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "my-token", tok)
	})

	t.Run("trimmed token", func(t *testing.T) {
		ts := StaticTokenSource("  spaced-token \n")
		tok, err := ts.Token(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "spaced-token", tok)
	})

	t.Run("empty token", func(t *testing.T) {
		ts := StaticTokenSource("   ")
		_, err := ts.Token(context.Background())
		assert.ErrorIs(t, err, ErrNoTunnelCredential)
	})
}

func TestFileTokenSource(t *testing.T) {
	tempDir := t.TempDir()
	tokenPath := filepath.Join(tempDir, "token")

	require.NoError(t, os.WriteFile(tokenPath, []byte("initial-token\n"), 0o600))
	fts := NewFileTokenSource(tokenPath)

	tok, err := fts.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "initial-token", tok)

	// Simulate token rotation on disk (e.g. Kubernetes projected token refresh)
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, os.WriteFile(tokenPath, []byte("rotated-token\n"), 0o600))
	// Artificially advance mod time in case filesystem timestamp resolution is coarse
	newModTime := time.Now().Add(1 * time.Second)
	require.NoError(t, os.Chtimes(tokenPath, newModTime, newModTime))

	fts.mu.Lock()
	fts.lastCheck = time.Time{} // force re-check
	fts.mu.Unlock()

	tok2, err := fts.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "rotated-token", tok2, "FileTokenSource must dynamically reload rotated token")

	t.Run("missing file returns error", func(t *testing.T) {
		badFts := NewFileTokenSource(filepath.Join(tempDir, "nonexistent"))
		_, err := badFts.Token(context.Background())
		require.Error(t, err)
	})

	t.Run("empty file returns error", func(t *testing.T) {
		emptyPath := filepath.Join(tempDir, "empty")
		require.NoError(t, os.WriteFile(emptyPath, []byte("   \n"), 0o600))
		emptyFts := NewFileTokenSource(emptyPath)
		_, err := emptyFts.Token(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty")
	})
}

func TestOpenBaoTokenSource(t *testing.T) {
	t.Run("BAO_TOKEN takes precedence over VAULT_TOKEN", func(t *testing.T) {
		t.Setenv("BAO_TOKEN", "bao-env")
		t.Setenv("VAULT_TOKEN", "vault-env")
		ts := NewOpenBaoTokenSource()
		tok, err := ts.Token(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "bao-env", tok)
	})

	t.Run("VAULT_TOKEN used if BAO_TOKEN unset", func(t *testing.T) {
		t.Setenv("BAO_TOKEN", "")
		t.Setenv("VAULT_TOKEN", "vault-env")
		ts := NewOpenBaoTokenSource()
		tok, err := ts.Token(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "vault-env", tok)
	})

	t.Run("reads from ~/.bao-token if env unset", func(t *testing.T) {
		t.Setenv("BAO_TOKEN", "")
		t.Setenv("VAULT_TOKEN", "")
		tempHome := t.TempDir()
		t.Setenv("HOME", tempHome)
		err := os.WriteFile(filepath.Join(tempHome, ".bao-token"), []byte("bao-home-tok\n"), 0o600)
		require.NoError(t, err)

		ts := NewOpenBaoTokenSource()
		tok, err := ts.Token(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "bao-home-tok", tok)
	})

	t.Run("reads from ~/.vault-token if ~/.bao-token absent", func(t *testing.T) {
		t.Setenv("BAO_TOKEN", "")
		t.Setenv("VAULT_TOKEN", "")
		tempHome := t.TempDir()
		t.Setenv("HOME", tempHome)
		err := os.WriteFile(filepath.Join(tempHome, ".vault-token"), []byte("vault-home-tok\n"), 0o600)
		require.NoError(t, err)

		ts := NewOpenBaoTokenSource()
		tok, err := ts.Token(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "vault-home-tok", tok)
	})

	t.Run("returns ErrNoTunnelCredential if neither env nor files present", func(t *testing.T) {
		t.Setenv("BAO_TOKEN", "")
		t.Setenv("VAULT_TOKEN", "")
		t.Setenv("HOME", t.TempDir())
		ts := NewOpenBaoTokenSource()
		_, err := ts.Token(context.Background())
		assert.ErrorIs(t, err, ErrNoTunnelCredential)
	})
}

func TestKubeTokenSource(t *testing.T) {
	t.Run("resolves bearer token from rest.Config", func(t *testing.T) {
		cfg := &rest.Config{
			BearerToken: "kube-bearer-tok",
		}
		kts, err := NewKubeTokenSource(cfg)
		require.NoError(t, err)
		tok, err := kts.Token(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "kube-bearer-tok", tok)
	})

	t.Run("empty rest.Config returns ErrNoTunnelCredential", func(t *testing.T) {
		cfg := &rest.Config{}
		kts, err := NewKubeTokenSource(cfg)
		require.NoError(t, err)
		_, err = kts.Token(context.Background())
		assert.ErrorIs(t, err, ErrNoTunnelCredential)
	})

	t.Run("nil rest.Config returns error", func(t *testing.T) {
		_, err := NewKubeTokenSource(nil)
		require.Error(t, err)
	})
}

func TestChainTokenSource(t *testing.T) {
	t.Run("first non-empty token is returned", func(t *testing.T) {
		chain := NewChainTokenSource(
			StaticTokenSource(""),
			StaticTokenSource("second-tok"),
			StaticTokenSource("third-tok"),
		)
		tok, err := chain.Token(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "second-tok", tok)
	})

	t.Run("all failing returns ErrNoTunnelCredential", func(t *testing.T) {
		chain := NewChainTokenSource(
			StaticTokenSource(""),
			StaticTokenSource("   "),
		)
		_, err := chain.Token(context.Background())
		assert.ErrorIs(t, err, ErrNoTunnelCredential)
	})
}

func TestResolveTunnelTokenSource(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("  file-token\n"), 0o600))

	tests := []struct {
		name     string
		explicit string
		env      string
		restCfg  *rest.Config
		want     string
		wantErr  error
	}{
		{
			name:     "explicit token wins over everything",
			explicit: "flag-token",
			env:      "env-token",
			restCfg:  &rest.Config{BearerToken: "kube-token"},
			want:     "flag-token",
		},
		{
			name:    "env var used when no flag",
			env:     "env-token",
			restCfg: &rest.Config{BearerToken: "kube-token"},
			want:    "env-token",
		},
		{
			name:    "bearer token file is read and takes precedence over static bearer token",
			restCfg: &rest.Config{BearerTokenFile: tokenFile, BearerToken: "static-token-ignored"},
			want:    "file-token",
		},
		{
			name:    "kubeconfig static bearer token is used when no file",
			restCfg: &rest.Config{BearerToken: "kube-token"},
			want:    "kube-token",
		},
		{
			name:     "surrounding whitespace is trimmed",
			explicit: "  spaced  ",
			want:     "spaced",
		},
		{
			name:    "no credential anywhere returns ErrNoTunnelCredential",
			restCfg: &rest.Config{},
			wantErr: ErrNoTunnelCredential,
		},
		{
			name:    "nil rest config returns ErrNoTunnelCredential",
			wantErr: ErrNoTunnelCredential,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tunnelTokenEnvVar, tt.env)
			t.Setenv("BAO_TOKEN", "ambient-bao")
			t.Setenv("VAULT_TOKEN", "ambient-vault")
			tempHome := t.TempDir()
			t.Setenv("HOME", tempHome)

			ts, err := resolveTunnelTokenSource(tt.explicit, tt.restCfg)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			tok, err := ts.Token(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tt.want, tok)

			// Also verify resolveTunnelToken helper returns identical result
			strTok, err := resolveTunnelToken(tt.explicit, tt.restCfg)
			require.NoError(t, err)
			assert.Equal(t, tt.want, strTok)
		})
	}

	t.Run("bearer token file takes precedence over static bearer token when both populated", func(t *testing.T) {
		tf := filepath.Join(t.TempDir(), "token")
		require.NoError(t, os.WriteFile(tf, []byte("file-token-wins\n"), 0o600))
		restCfg := &rest.Config{
			BearerTokenFile: tf,
			BearerToken:     "static-token-ignored",
		}
		ts, err := resolveTunnelTokenSource("", restCfg)
		require.NoError(t, err)
		tok, err := ts.Token(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "file-token-wins", tok)
	})

	t.Run("openbao and vault tokens are ignored for tunnel credentials", func(t *testing.T) {
		t.Setenv(tunnelTokenEnvVar, "")
		t.Setenv("BAO_TOKEN", "bao-token")
		t.Setenv("VAULT_TOKEN", "vault-token")
		t.Setenv("HOME", t.TempDir())
		_, err := resolveTunnelTokenSource("", &rest.Config{})
		assert.ErrorIs(t, err, ErrNoTunnelCredential)
	})
}

func TestResolveTunnelToken_PBT(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		tokenGen := rapid.StringMatching(`[a-zA-Z0-9_\-\.]{1,40}`)
		wsGen := rapid.StringMatching(`[ \t\r\n]{0,4}`)

		cleanExplicit := tokenGen.Draw(rt, "cleanExplicit")
		explicit := wsGen.Draw(rt, "wsPre1") + cleanExplicit + wsGen.Draw(rt, "wsPost1")
		envToken := wsGen.Draw(rt, "wsPre2") + tokenGen.Draw(rt, "cleanEnv") + wsGen.Draw(rt, "wsPost2")
		fileToken := wsGen.Draw(rt, "wsPreFile") + tokenGen.Draw(rt, "cleanFile") + wsGen.Draw(rt, "wsPostFile")
		kubeToken := wsGen.Draw(rt, "wsPre3") + tokenGen.Draw(rt, "cleanKube") + wsGen.Draw(rt, "wsPost3")

		tokenFile := filepath.Join(t.TempDir(), "token")
		require.NoError(t, os.WriteFile(tokenFile, []byte(fileToken), 0o600))

		t.Setenv(tunnelTokenEnvVar, envToken)
		t.Setenv("BAO_TOKEN", "ambient-bao-token")
		t.Setenv("VAULT_TOKEN", "ambient-vault-token")
		t.Setenv("HOME", t.TempDir())
		restCfg := &rest.Config{
			BearerTokenFile: tokenFile,
			BearerToken:     kubeToken,
		}

		// Property 1: Explicit non-whitespace token always wins over env, file, and static
		got, err := resolveTunnelToken(explicit, restCfg)
		require.NoError(t, err)
		assert.Equal(t, cleanExplicit, got, "explicit token must win over env and kubeconfig")
		assert.Equal(t, strings.TrimSpace(got), got, "token must never have surrounding whitespace")

		// Property 2: When explicit is whitespace-only, DIVERGE_TOKEN wins over file and static
		onlyWS := wsGen.Draw(rt, "onlyWS")
		got, err = resolveTunnelToken(onlyWS, restCfg)
		require.NoError(t, err)
		assert.Equal(t, strings.TrimSpace(envToken), got, "env token must win when explicit token is whitespace-only")
		assert.Equal(t, strings.TrimSpace(got), got)

		// Property 3: When explicit and DIVERGE_TOKEN are whitespace-only, BearerTokenFile wins over static BearerToken
		t.Setenv(tunnelTokenEnvVar, onlyWS)
		got, err = resolveTunnelToken(onlyWS, restCfg)
		require.NoError(t, err)
		assert.Equal(t, strings.TrimSpace(fileToken), got, "file token must win over static bearer token when flag and DIVERGE_TOKEN are absent")
		assert.Equal(t, strings.TrimSpace(got), got)

		// Property 4: When BearerTokenFile is empty, static BearerToken is used
		staticOnlyRestCfg := &rest.Config{BearerToken: kubeToken}
		got, err = resolveTunnelToken(onlyWS, staticOnlyRestCfg)
		require.NoError(t, err)
		assert.Equal(t, strings.TrimSpace(kubeToken), got, "kubeconfig static token must win when file token is absent")
		assert.Equal(t, strings.TrimSpace(got), got)

		// Property 5: When all sources lack a credential (even with ambient BAO_TOKEN), ErrNoTunnelCredential is returned
		emptyRestCfg := &rest.Config{BearerToken: onlyWS}
		_, err = resolveTunnelToken(onlyWS, emptyRestCfg)
		assert.ErrorIs(t, err, ErrNoTunnelCredential)

		_, err = resolveTunnelToken(onlyWS, nil)
		assert.ErrorIs(t, err, ErrNoTunnelCredential)
	})
}
