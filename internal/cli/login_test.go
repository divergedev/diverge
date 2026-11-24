package cli

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/divergedev/diverge/internal/server/auth"
	"github.com/divergedev/diverge/internal/tunnelclient"
)

type mockPKCEOIDCServer struct {
	t                    *testing.T
	key                  *rsa.PrivateKey
	issuerURL            string
	pathPrefix           string
	overrideAuthEndpoint string
	srv                  *httptest.Server
	mu                   sync.Mutex
	authCodes            map[string]mockAuthCodeRecord
	refreshCount         int
	tokenCounter         int
	overrideNonce        string
	omitIDToken          bool
	refreshSubOverride   string
	refreshErrCode       string
	failDiscovery        bool
	failJWKS             bool
	requireRefreshToken  string
	lastPrompt           string
}

type mockAuthCodeRecord struct {
	clientID      string
	redirectURI   string
	codeChallenge string
	nonce         string
	scopes        string
}

func newMockPKCEOIDCServer(t *testing.T) *mockPKCEOIDCServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	m := &mockPKCEOIDCServer{
		t:         t,
		key:       key,
		authCodes: make(map[string]mockAuthCodeRecord),
	}

	m.srv = httptest.NewServer(http.HandlerFunc(m.handleHTTP))
	m.issuerURL = m.srv.URL
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockPKCEOIDCServer) signIDToken(claims map[string]any) string {
	m.t.Helper()
	hdrJSON, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "cli-test-kid", "typ": "JWT"})
	payloadJSON, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(hdrJSON) + "." + base64.RawURLEncoding.EncodeToString(payloadJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, m.key, crypto.SHA256, digest[:])
	require.NoError(m.t, err)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (m *mockPKCEOIDCServer) handleHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	reqPath := r.URL.Path
	if m.pathPrefix != "" {
		reqPath = strings.TrimPrefix(reqPath, m.pathPrefix)
	}
	baseURL := strings.TrimSuffix(m.issuerURL, "/")
	authEP := m.overrideAuthEndpoint
	if authEP == "" {
		authEP = baseURL + "/authorize"
	}
	switch reqPath {
	case "/.well-known/openid-configuration":
		m.mu.Lock()
		failDisc := m.failDiscovery
		m.mu.Unlock()
		if failDisc {
			http.Error(w, "discovery temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                m.issuerURL,
			"jwks_uri":                              baseURL + "/keys",
			"authorization_endpoint":                authEP,
			"token_endpoint":                        baseURL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	case "/keys":
		m.mu.Lock()
		failJWKS := m.failJWKS
		m.mu.Unlock()
		if failJWKS {
			http.Error(w, "jwks temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		eBytes := big.NewInt(int64(m.key.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"kid": "cli-test-kid",
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(m.key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(eBytes),
			}},
		})
	case "/authorize":
		q := r.URL.Query()
		if q.Get("response_type") != "code" {
			http.Error(w, "invalid response_type", http.StatusBadRequest)
			return
		}
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "missing PKCE S256 challenge", http.StatusBadRequest)
			return
		}
		code := "authcode-1"
		m.mu.Lock()
		m.lastPrompt = q.Get("prompt")
		m.authCodes[code] = mockAuthCodeRecord{
			clientID:      q.Get("client_id"),
			redirectURI:   q.Get("redirect_uri"),
			codeChallenge: q.Get("code_challenge"),
			nonce:         q.Get("nonce"),
			scopes:        q.Get("scope"),
		}
		m.mu.Unlock()
		redir, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			http.Error(w, "bad redirect_uri", http.StatusBadRequest)
			return
		}
		rq := redir.Query()
		rq.Set("code", code)
		rq.Set("state", q.Get("state"))
		redir.RawQuery = rq.Encode()
		http.Redirect(w, r, redir.String(), http.StatusFound)
	case "/token":
		require.NoError(m.t, r.ParseForm())
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			code := r.Form.Get("code")
			verifier := r.Form.Get("code_verifier")
			m.mu.Lock()
			rec, ok := m.authCodes[code]
			delete(m.authCodes, code)
			m.tokenCounter++
			n := m.tokenCounter
			overrideNonce := m.overrideNonce
			omitID := m.omitIDToken
			m.mu.Unlock()
			if !ok {
				http.Error(w, "invalid code", http.StatusBadRequest)
				return
			}
			sum := sha256.Sum256([]byte(verifier))
			gotChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
			if gotChallenge != rec.codeChallenge {
				http.Error(w, "PKCE verifier mismatch", http.StatusBadRequest)
				return
			}
			if r.Form.Get("redirect_uri") != rec.redirectURI {
				http.Error(w, "redirect_uri mismatch", http.StatusBadRequest)
				return
			}
			nonce := rec.nonce
			if overrideNonce != "" {
				nonce = overrideNonce
			}
			resp := map[string]any{
				"access_token":  "access-tok-1",
				"token_type":    "Bearer",
				"expires_in":    3600,
				"refresh_token": "refresh-tok-1",
			}
			if !omitID {
				resp["id_token"] = m.signIDToken(map[string]any{
					"iss":                m.issuerURL,
					"aud":                rec.clientID,
					"sub":                "sub-alice",
					"nonce":              nonce,
					"preferred_username": "alice",
					"email":              "alice@example.com",
					"groups":             []string{"engineering", "developers"},
					"jti":                time.Now().UnixNano() + int64(n),
					"exp":                time.Now().Add(time.Hour).Unix(),
					"iat":                time.Now().Unix(),
				})
			}
			_ = json.NewEncoder(w).Encode(resp)

		case "refresh_token":
			rt := r.Form.Get("refresh_token")
			if rt == "" {
				http.Error(w, "missing refresh_token", http.StatusBadRequest)
				return
			}
			m.mu.Lock()
			errCode := m.refreshErrCode
			subOverride := m.refreshSubOverride
			reqRT := m.requireRefreshToken
			m.refreshCount++
			refreshNum := m.refreshCount
			m.tokenCounter++
			cnt := m.tokenCounter
			m.mu.Unlock()
			if errCode != "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":             errCode,
					"error_description": "refresh rejected",
				})
				return
			}
			if reqRT != "" && rt != reqRT {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":             "invalid_grant",
					"error_description": "refresh token wrong or revoked",
				})
				return
			}
			sub := "sub-alice"
			if subOverride != "" {
				sub = subOverride
			}
			rawID := m.signIDToken(map[string]any{
				"iss":                m.issuerURL,
				"aud":                r.Form.Get("client_id"),
				"sub":                sub,
				"preferred_username": "alice",
				"email":              "alice@example.com",
				"groups":             []string{"engineering", "developers"},
				"jti":                cnt,
				"exp":                time.Now().Add(2 * time.Hour).Unix(),
				"iat":                time.Now().Unix(),
			})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  fmt.Sprintf("access-tok-refreshed-%d", refreshNum),
				"token_type":    "Bearer",
				"expires_in":    7200,
				"refresh_token": fmt.Sprintf("refresh-tok-%d", refreshNum+1),
				"id_token":      rawID,
			})
		default:
			http.Error(w, "unsupported grant_type", http.StatusBadRequest)
		}
	default:
		http.NotFound(w, r)
	}
}

func startMockDivergeAuthConfigServer(t *testing.T, cfg cliAuthConfig) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/config", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cfg)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestLoginCmd_PKCEFlowDualAudienceAndAutoRefresh(t *testing.T) {
	t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig-does-not-exist")
	t.Setenv(tunnelTokenEnvVar, "")
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	idp := newMockPKCEOIDCServer(t)
	divergeSrv := startMockDivergeAuthConfigServer(t, cliAuthConfig{
		OIDCEnabled:  true,
		ProviderName: "TestSSO",
		IssuerURL:    idp.issuerURL,
		CLIClientID:  "diverge-cli",
		Scopes:       []string{"openid", "profile", "email", "groups"},
	})

	storePath := filepath.Join(xdgDir, "diverge", "credentials.json")

	// Simulate user browser visiting the PKCE authURL.
	browserDone := make(chan error, 1)
	simulateBrowser := func(authURL string) error {
		go func() {
			client := &http.Client{
				Timeout: 5 * time.Second,
			}
			// Do not follow the final loopback redirect automatically until after checking state mismatch safety first.
			resp, err := client.Get(authURL)
			if err != nil {
				browserDone <- err
				return
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				browserDone <- assert.AnError
				return
			}
			if !bytes.Contains(body, []byte("Callback Received")) || !bytes.Contains(body, []byte("terminal")) {
				browserDone <- assert.AnError
				return
			}
			browserDone <- nil
		}()
		return nil
	}

	var stdout, stderr bytes.Buffer
	app := &App{}
	cmd := newLoginCmdWithDeps(app, &loginDeps{
		credStorePath: storePath,
		openBrowser:   simulateBrowser,
	})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--server", divergeSrv.URL, "--timeout", "10s"})

	require.NoError(t, cmd.ExecuteContext(t.Context()))
	require.NoError(t, <-browserDone)
	assert.Contains(t, stdout.String(), "Logged in to "+divergeSrv.URL+" as alice")

	// Verify file permissions are 0600 (0700 parent dir).
	dirInfo, err := os.Stat(filepath.Dir(storePath))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
	fileInfo, err := os.Stat(storePath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())

	store, err := LoadCredentialStore(storePath)
	require.NoError(t, err)
	cred, ok := store.Servers[normalizeServerKey(divergeSrv.URL)]
	require.True(t, ok)
	assert.Equal(t, "diverge-cli", cred.ClientID)
	assert.Equal(t, "alice", cred.Username)
	assert.Equal(t, "alice@example.com", cred.Email)
	assert.Equal(t, "refresh-tok-1", cred.RefreshToken)
	initialIDTok := cred.IDToken
	require.NotEmpty(t, initialIDTok)

	// Verify server-side OIDCProvider accepts tokens audited for CLIClientID alongside ClientID.
	serverOIDC, err := auth.NewOIDCProvider(t.Context(), auth.OIDCProviderConfig{
		IssuerURL:     idp.issuerURL,
		ClientID:      "diverge-web",
		CLIClientID:   "diverge-cli",
		AllowedGroups: []string{"engineering"},
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	userInfo, err := serverOIDC.Authenticate(t.Context(), initialIDTok)
	require.NoError(t, err)
	assert.Equal(t, "oidc:alice", userInfo.Username)
	assert.Equal(t, []string{"oidc:engineering", "oidc:developers"}, userInfo.Groups)

	// Verify resolveStandaloneTunnelTokenSourceWithStore automatically picks up stored credential.
	tokSrc, err := resolveStandaloneTunnelTokenSourceWithStore(t.Context(), divergeSrv.URL, "", "", storePath, nil)
	require.NoError(t, err)

	tok1, err := tokSrc.Token(t.Context())
	require.NoError(t, err)
	assert.Equal(t, initialIDTok, tok1)

	// Cached read should not invoke refresh endpoint.
	idp.mu.Lock()
	assert.Equal(t, 0, idp.refreshCount)
	idp.mu.Unlock()

	// Invalidate forces refresh_token exchange, persists rotated token, and authenticates on server.
	inv, ok := tokSrc.(InvalidatableTokenSource)
	require.True(t, ok)
	inv.Invalidate()

	tok2, err := tokSrc.Token(t.Context())
	require.NoError(t, err)
	assert.NotEqual(t, initialIDTok, tok2)

	idp.mu.Lock()
	assert.Equal(t, 1, idp.refreshCount)
	idp.mu.Unlock()

	updatedStore, err := LoadCredentialStore(storePath)
	require.NoError(t, err)
	assert.Equal(t, tok2, updatedStore.Servers[normalizeServerKey(divergeSrv.URL)].IDToken)
	assert.Equal(t, "refresh-tok-2", updatedStore.Servers[normalizeServerKey(divergeSrv.URL)].RefreshToken)

	userInfo2, err := serverOIDC.Authenticate(t.Context(), tok2)
	require.NoError(t, err)
	assert.Equal(t, "oidc:alice", userInfo2.Username)

	// Test `diverge login status` and `diverge login clear`.
	var statusOut bytes.Buffer
	statusCmd := newLoginCmdWithDeps(&App{}, &loginDeps{credStorePath: storePath})
	statusCmd.SetOut(&statusOut)
	statusCmd.SetArgs([]string{"status", "--json"})
	require.NoError(t, statusCmd.ExecuteContext(t.Context()))
	var entries []loginStatusEntry
	require.NoError(t, json.Unmarshal(statusOut.Bytes(), &entries))
	require.Len(t, entries, 1)
	assert.Equal(t, divergeSrv.URL, entries[0].Server)
	assert.True(t, entries[0].Refreshable)

	var clearOut bytes.Buffer
	clearCmd := newLoginCmdWithDeps(&App{}, &loginDeps{credStorePath: storePath})
	clearCmd.SetOut(&clearOut)
	clearCmd.SetArgs([]string{"clear", "--server", divergeSrv.URL})
	require.NoError(t, clearCmd.ExecuteContext(t.Context()))
	assert.Contains(t, clearOut.String(), "Cleared stored credentials")
	assert.False(t, HasStoredCredential(storePath, divergeSrv.URL))
}

func TestLoginCmd_SecurityAndErrorHandling(t *testing.T) {
	t.Run("refuses non-loopback http:// server", func(t *testing.T) {
		cmd := newLoginCmdWithDeps(&App{}, &loginDeps{credStorePath: filepath.Join(t.TempDir(), "creds.json")})
		cmd.SetArgs([]string{"--server", "http://diverge.example.com", "--no-browser"})
		err := cmd.ExecuteContext(t.Context())
		require.Error(t, err)
		assert.ErrorIs(t, err, tunnelclient.ErrInsecureHTTP)
	})

	t.Run("errors clearly when OIDC disabled", func(t *testing.T) {
		srv := startMockDivergeAuthConfigServer(t, cliAuthConfig{OIDCEnabled: false})
		cmd := newLoginCmdWithDeps(&App{}, &loginDeps{credStorePath: filepath.Join(t.TempDir(), "creds.json")})
		cmd.SetArgs([]string{"--server", srv.URL, "--no-browser"})
		err := cmd.ExecuteContext(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "OIDC authentication is disabled")
	})

	t.Run("rejects nonce mismatch on ID token", func(t *testing.T) {
		idp := newMockPKCEOIDCServer(t)
		idp.overrideNonce = "wrong-nonce"
		srv := startMockDivergeAuthConfigServer(t, cliAuthConfig{
			OIDCEnabled: true,
			IssuerURL:   idp.issuerURL,
			CLIClientID: "diverge-cli",
		})
		cmd := newLoginCmdWithDeps(&App{}, &loginDeps{
			credStorePath: filepath.Join(t.TempDir(), "creds.json"),
			openBrowser: func(authURL string) error {
				go func() {
					resp, err := http.Get(authURL) // #nosec G107 -- test URL
					if err == nil {
						_ = resp.Body.Close()
					}
				}()
				return nil
			},
		})
		cmd.SetOut(io.Discard)
		cmd.SetArgs([]string{"--server", srv.URL, "--timeout", "5s"})
		err := cmd.ExecuteContext(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nonce mismatch")
	})

	t.Run("ignores state mismatch request then accepts valid callback", func(t *testing.T) {
		idp := newMockPKCEOIDCServer(t)
		srv := startMockDivergeAuthConfigServer(t, cliAuthConfig{
			OIDCEnabled: true,
			IssuerURL:   idp.issuerURL,
			CLIClientID: "diverge-cli",
		})
		storePath := filepath.Join(t.TempDir(), "creds.json")
		var badStatus int
		var spoofErrStatus int
		done := make(chan struct{})
		cmd := newLoginCmdWithDeps(&App{}, &loginDeps{
			credStorePath: storePath,
			openBrowser: func(authURL string) error {
				go func() {
					defer close(done)
					u, _ := url.Parse(authURL)
					redirURL, _ := url.Parse(u.Query().Get("redirect_uri"))
					badResp, err := http.Get(redirURL.String() + "?state=tampered&code=fake") // #nosec G107 -- test URL
					if err == nil {
						badStatus = badResp.StatusCode
						_ = badResp.Body.Close()
					}
					// Unauthenticated error without matching state must NOT abort the in-flight login.
					spoofResp, err := http.Get(redirURL.String() + "?error=access_denied&error_description=spoofed") // #nosec G107 -- test URL
					if err == nil {
						spoofErrStatus = spoofResp.StatusCode
						_ = spoofResp.Body.Close()
					}
					goodResp, err := http.Get(authURL) // #nosec G107 -- test URL
					if err == nil {
						_ = goodResp.Body.Close()
					}
				}()
				return nil
			},
		})
		cmd.SetOut(io.Discard)
		cmd.SetArgs([]string{"--server", srv.URL, "--timeout", "5s"})
		require.NoError(t, cmd.ExecuteContext(t.Context()))
		<-done
		assert.Equal(t, http.StatusBadRequest, badStatus)
		assert.Equal(t, http.StatusBadRequest, spoofErrStatus)
		assert.True(t, HasStoredCredential(storePath, srv.URL))
	})

	t.Run("surfaces OIDC provider error parameter", func(t *testing.T) {
		idp := newMockPKCEOIDCServer(t)
		srv := startMockDivergeAuthConfigServer(t, cliAuthConfig{
			OIDCEnabled: true,
			IssuerURL:   idp.issuerURL,
			CLIClientID: "diverge-cli",
		})
		cmd := newLoginCmdWithDeps(&App{}, &loginDeps{
			credStorePath: filepath.Join(t.TempDir(), "creds.json"),
			openBrowser: func(authURL string) error {
				go func() {
					u, _ := url.Parse(authURL)
					state := u.Query().Get("state")
					redir := u.Query().Get("redirect_uri") + "?state=" + url.QueryEscape(state) + "&error=access_denied&error_description=denied"
					resp, err := http.Get(redir) // #nosec G107 -- test URL
					if err == nil {
						_ = resp.Body.Close()
					}
				}()
				return nil
			},
		})
		cmd.SetOut(io.Discard)
		cmd.SetArgs([]string{"--server", srv.URL, "--timeout", "5s"})
		err := cmd.ExecuteContext(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "access_denied")
	})

	t.Run("expired stored token without refresh_token returns actionable error", func(t *testing.T) {
		storePath := filepath.Join(t.TempDir(), "creds.json")
		require.NoError(t, SaveCredentialStore(storePath, &CredentialStore{
			Version: 1,
			Servers: map[string]ServerCredential{
				"https://diverge.example.com": {
					IssuerURL: "https://idp.example.com",
					ClientID:  "diverge-cli",
					TokenURL:  "https://idp.example.com/token",
					IDToken:   "expired-id-tok",
					ExpiresAt: time.Now().Add(-time.Hour),
				},
			},
		}))
		src := NewStoredCredentialTokenSource(storePath, "https://diverge.example.com", nil)
		_, err := src.Token(context.Background())
		require.Error(t, err)
		assert.ErrorIs(t, err, tunnelclient.ErrAuthRequired)
		assert.Contains(t, err.Error(), "diverge login")
	})
}

func TestLoginCmd_HardeningAndClearPrecedence(t *testing.T) {
	t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	t.Run("preserves trailing slash in issuerUrl for OIDC discovery", func(t *testing.T) {
		idp := newMockPKCEOIDCServer(t)
		idp.pathPrefix = "/tenant"
		idp.issuerURL = idp.srv.URL + "/tenant/"
		srv := startMockDivergeAuthConfigServer(t, cliAuthConfig{
			OIDCEnabled: true,
			IssuerURL:   idp.issuerURL,
			CLIClientID: "diverge-cli",
		})
		storePath := filepath.Join(t.TempDir(), "creds.json")
		cmd := newLoginCmdWithDeps(&App{}, &loginDeps{
			credStorePath: storePath,
			openBrowser: func(authURL string) error {
				go func() {
					resp, err := http.Get(authURL) // #nosec G107 -- test URL
					if err == nil {
						_ = resp.Body.Close()
					}
				}()
				return nil
			},
		})
		cmd.SetOut(io.Discard)
		cmd.SetArgs([]string{"--server", srv.URL, "--timeout", "5s"})
		require.NoError(t, cmd.ExecuteContext(t.Context()))

		store, err := LoadCredentialStore(storePath)
		require.NoError(t, err)
		cred := store.Servers[normalizeServerKey(srv.URL)]
		assert.Equal(t, idp.issuerURL, cred.IssuerURL)
		assert.Equal(t, "sub-alice", cred.Subject)
	})

	t.Run("rejects unsafe authorization_endpoint before opening browser", func(t *testing.T) {
		for _, badEndpoint := range []string{"file:///etc/passwd", "http://evil.example.com/authorize"} {
			idp := newMockPKCEOIDCServer(t)
			idp.overrideAuthEndpoint = badEndpoint
			srv := startMockDivergeAuthConfigServer(t, cliAuthConfig{
				OIDCEnabled: true,
				IssuerURL:   idp.issuerURL,
				CLIClientID: "diverge-cli",
			})
			browserCalled := false
			cmd := newLoginCmdWithDeps(&App{}, &loginDeps{
				credStorePath: filepath.Join(t.TempDir(), "creds.json"),
				openBrowser: func(string) error {
					browserCalled = true
					return nil
				},
			})
			cmd.SetOut(io.Discard)
			cmd.SetArgs([]string{"--server", srv.URL, "--timeout", "5s"})
			err := cmd.ExecuteContext(t.Context())
			require.Error(t, err)
			assert.False(t, browserCalled, "browser must not launch for unsafe endpoint %s", badEndpoint)
		}
	})

	t.Run("rejects malformed --callback-path without panicking", func(t *testing.T) {
		for _, badPath := range []string{"/cb?x=1", "/cb/{bad", "//evil", "/cb space", "noslash"} {
			cmd := newLoginCmdWithDeps(&App{}, &loginDeps{credStorePath: filepath.Join(t.TempDir(), "creds.json")})
			cmd.SetArgs([]string{"--server", "https://diverge.example.com", "--callback-path", badPath})
			err := cmd.ExecuteContext(t.Context())
			require.Error(t, err, "path %q must fail", badPath)
			assert.Contains(t, err.Error(), "--callback-path")
		}
	})

	t.Run("refresh enforces sub continuity", func(t *testing.T) {
		idp := newMockPKCEOIDCServer(t)
		idp.refreshSubOverride = "sub-attacker"
		storePath := filepath.Join(t.TempDir(), "creds.json")
		require.NoError(t, SaveCredentialStore(storePath, &CredentialStore{
			Version: 1,
			Servers: map[string]ServerCredential{
				"https://diverge.example.com": {
					IssuerURL:    idp.issuerURL,
					ClientID:     "diverge-cli",
					Subject:      "sub-alice",
					TokenURL:     idp.issuerURL + "/token",
					IDToken:      "expired-id-tok",
					RefreshToken: "refresh-tok-1",
					ExpiresAt:    time.Now().Add(-time.Hour),
				},
			},
		}))

		src := NewStoredCredentialTokenSource(storePath, "https://diverge.example.com", nil)
		_, err := src.Token(t.Context())
		require.Error(t, err)
		assert.ErrorIs(t, err, tunnelclient.ErrAuthRequired)
		assert.Contains(t, err.Error(), "subject")
	})

	t.Run("refresh wraps invalid_grant as ErrAuthRequired and preserves RetrieveError", func(t *testing.T) {
		idp := newMockPKCEOIDCServer(t)
		idp.refreshErrCode = "invalid_grant"
		storePath := filepath.Join(t.TempDir(), "creds.json")
		require.NoError(t, SaveCredentialStore(storePath, &CredentialStore{
			Version: 1,
			Servers: map[string]ServerCredential{
				"https://diverge.example.com": {
					IssuerURL:    idp.issuerURL,
					ClientID:     "diverge-cli",
					Subject:      "sub-alice",
					TokenURL:     idp.issuerURL + "/token",
					IDToken:      "expired-id-tok",
					RefreshToken: "refresh-tok-1",
					ExpiresAt:    time.Now().Add(-time.Hour),
				},
			},
		}))

		src := NewStoredCredentialTokenSource(storePath, "https://diverge.example.com", nil)
		_, err := src.Token(t.Context())
		require.Error(t, err)
		assert.ErrorIs(t, err, tunnelclient.ErrAuthRequired)
		var retrieveErr *oauth2.RetrieveError
		require.ErrorAs(t, err, &retrieveErr)
		assert.Equal(t, "invalid_grant", retrieveErr.ErrorCode)
	})

	t.Run("adds prompt=consent only when scopes include offline_access", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			scopesFlag string
			wantPrompt string
		}{
			{name: "online only", scopesFlag: "openid,profile,email", wantPrompt: ""},
			{name: "offline access", scopesFlag: "openid,profile,offline_access", wantPrompt: "consent"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				idp := newMockPKCEOIDCServer(t)
				srv := startMockDivergeAuthConfigServer(t, cliAuthConfig{
					OIDCEnabled: true,
					IssuerURL:   idp.issuerURL,
					CLIClientID: "diverge-cli",
				})
				cmd := newLoginCmdWithDeps(&App{}, &loginDeps{
					credStorePath: filepath.Join(t.TempDir(), "creds.json"),
					openBrowser: func(authURL string) error {
						go func() {
							resp, err := http.Get(authURL) // #nosec G107 -- test URL
							if err == nil {
								_ = resp.Body.Close()
							}
						}()
						return nil
					},
				})
				cmd.SetOut(io.Discard)
				cmd.SetArgs([]string{"--server", srv.URL, "--scopes", tc.scopesFlag, "--timeout", "5s"})
				require.NoError(t, cmd.ExecuteContext(t.Context()))

				idp.mu.Lock()
				gotPrompt := idp.lastPrompt
				idp.mu.Unlock()
				assert.Equal(t, tc.wantPrompt, gotPrompt)
			})
		}
	})

	t.Run("validateCallbackPath rejects dot segments while keeping clean and trailing-slash paths", func(t *testing.T) {
		for _, bad := range []string{"/auth/../callback", "/auth/./callback", "/.", "/..", "/auth/callback/."} {
			_, err := validateCallbackPath(bad)
			require.Error(t, err, "dot segment path %q must fail", bad)
			assert.Contains(t, err.Error(), "--callback-path")
		}
		for _, good := range []string{"/auth/callback", "/auth/callback/", "/v1.0/callback"} {
			got, err := validateCallbackPath(good)
			require.NoError(t, err, "clean path %q must succeed", good)
			assert.Equal(t, good, got)
		}
	})

	t.Run("non-refreshable unexpired ID token within leeway stays usable until invalidated", func(t *testing.T) {
		storePath := filepath.Join(t.TempDir(), "creds.json")
		require.NoError(t, SaveCredentialStore(storePath, &CredentialStore{
			Version: 1,
			Servers: map[string]ServerCredential{
				"https://diverge.example.com": {
					IssuerURL: "https://idp.example.com",
					ClientID:  "diverge-cli",
					TokenURL:  "https://idp.example.com/token",
					IDToken:   "short-lived-id-tok",
					ExpiresAt: time.Now().Add(30 * time.Second),
				},
			},
		}))

		src := NewStoredCredentialTokenSource(storePath, "https://diverge.example.com", nil)
		// On-disk path
		tok, err := src.Token(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "short-lived-id-tok", tok)
		// Cached path
		tokCached, err := src.Token(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "short-lived-id-tok", tokCached)

		// Invalidating explicitly rejects reuse and requires re-login without a refresh token.
		src.Invalidate()
		_, err = src.Token(t.Context())
		require.ErrorIs(t, err, tunnelclient.ErrAuthRequired)

		// Contrast: with a refresh token present, 30s remaining (< 60s leeway) triggers refresh.
		idp := newMockPKCEOIDCServer(t)
		refreshStorePath := filepath.Join(t.TempDir(), "creds-refresh.json")
		require.NoError(t, SaveCredentialStore(refreshStorePath, &CredentialStore{
			Version: 1,
			Servers: map[string]ServerCredential{
				"https://diverge.example.com": {
					IssuerURL:    idp.issuerURL,
					ClientID:     "diverge-cli",
					Subject:      "sub-alice",
					TokenURL:     idp.issuerURL + "/token",
					IDToken:      "expiring-soon-id-tok",
					RefreshToken: "refresh-tok-1",
					ExpiresAt:    time.Now().Add(30 * time.Second),
				},
			},
		}))
		refreshSrc := NewStoredCredentialTokenSource(refreshStorePath, "https://diverge.example.com", nil)
		refreshedTok, err := refreshSrc.Token(t.Context())
		require.NoError(t, err)
		assert.NotEqual(t, "expiring-soon-id-tok", refreshedTok)
		idp.mu.Lock()
		assert.Equal(t, 1, idp.refreshCount)
		idp.mu.Unlock()
	})

	t.Run("discovery failure precedes token exchange so refresh token is not consumed", func(t *testing.T) {
		idp := newMockPKCEOIDCServer(t)
		idp.failDiscovery = true
		idp.requireRefreshToken = "refresh-tok-1"
		storePath := filepath.Join(t.TempDir(), "creds.json")
		require.NoError(t, SaveCredentialStore(storePath, &CredentialStore{
			Version: 1,
			Servers: map[string]ServerCredential{
				"https://diverge.example.com": {
					IssuerURL:    idp.issuerURL,
					ClientID:     "diverge-cli",
					Subject:      "sub-alice",
					TokenURL:     idp.issuerURL + "/token",
					IDToken:      "expired-id-tok",
					RefreshToken: "refresh-tok-1",
					ExpiresAt:    time.Now().Add(-time.Hour),
				},
			},
		}))

		src := NewStoredCredentialTokenSource(storePath, "https://diverge.example.com", nil)
		_, err := src.Token(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "discovery")
		idp.mu.Lock()
		assert.Equal(t, 0, idp.refreshCount, "discovery failure must abort before calling /token")
		idp.mu.Unlock()

		idp.failDiscovery = false
		tok, err := src.Token(t.Context())
		require.NoError(t, err)
		assert.NotEmpty(t, tok)
		idp.mu.Lock()
		assert.Equal(t, 1, idp.refreshCount)
		idp.mu.Unlock()
	})

	t.Run("JWKS verification failure preserves rotated refresh grant without replaying old refresh token", func(t *testing.T) {
		idp := newMockPKCEOIDCServer(t)
		idp.failJWKS = true
		idp.requireRefreshToken = "refresh-tok-1"
		storePath := filepath.Join(t.TempDir(), "creds.json")
		require.NoError(t, SaveCredentialStore(storePath, &CredentialStore{
			Version: 1,
			Servers: map[string]ServerCredential{
				"https://diverge.example.com": {
					IssuerURL:    idp.issuerURL,
					ClientID:     "diverge-cli",
					Subject:      "sub-alice",
					TokenURL:     idp.issuerURL + "/token",
					IDToken:      "expired-id-tok",
					RefreshToken: "refresh-tok-1",
					ExpiresAt:    time.Now().Add(-time.Hour),
				},
			},
		}))

		src := NewStoredCredentialTokenSource(storePath, "https://diverge.example.com", nil)
		_, err := src.Token(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "verification failed")

		// Verify R2 and PendingIDToken were persisted while expired IDToken was NOT overwritten with unverified token.
		midStore, err := LoadCredentialStore(storePath)
		require.NoError(t, err)
		midCred := midStore.Servers["https://diverge.example.com"]
		assert.Equal(t, "expired-id-tok", midCred.IDToken)
		assert.Equal(t, "refresh-tok-2", midCred.RefreshToken)
		require.NotEmpty(t, midCred.PendingIDToken)
		pendingID2 := midCred.PendingIDToken

		// Simulate IdP revoking refresh-tok-1 and JWKS recovering.
		idp.failJWKS = false
		idp.requireRefreshToken = "refresh-tok-2"

		tok, err := src.Token(t.Context())
		require.NoError(t, err)
		assert.Equal(t, pendingID2, tok)
		idp.mu.Lock()
		assert.Equal(t, 1, idp.refreshCount, "retry must verify PendingIDToken without replaying /token")
		idp.mu.Unlock()

		afterStore, err := LoadCredentialStore(storePath)
		require.NoError(t, err)
		afterCred := afterStore.Servers["https://diverge.example.com"]
		assert.Equal(t, pendingID2, afterCred.IDToken)
		assert.Empty(t, afterCred.PendingIDToken)
		assert.Equal(t, "refresh-tok-2", afterCred.RefreshToken)

		// Next invalidation must exchange using the preserved refresh-tok-2.
		src.Invalidate()
		tok3, err := src.Token(t.Context())
		require.NoError(t, err)
		assert.NotEqual(t, pendingID2, tok3)
		idp.mu.Lock()
		assert.Equal(t, 2, idp.refreshCount)
		idp.mu.Unlock()
	})

	t.Run("login clear --all recovers corrupt and missing credential stores", func(t *testing.T) {
		storePath := filepath.Join(t.TempDir(), "credentials.json")
		require.NoError(t, os.WriteFile(storePath, []byte("{not-json"), 0o600))

		var out bytes.Buffer
		cmd := newLoginCmdWithDeps(&App{}, &loginDeps{credStorePath: storePath})
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"clear", "--all"})
		require.NoError(t, cmd.ExecuteContext(t.Context()))
		assert.Contains(t, out.String(), "Cleared all stored credentials")
		_, statErr := os.Stat(storePath)
		assert.True(t, os.IsNotExist(statErr), "corrupt store file must be removed")

		// Missing store is also treated as success.
		out.Reset()
		cmd2 := newLoginCmdWithDeps(&App{}, &loginDeps{credStorePath: storePath})
		cmd2.SetOut(&out)
		cmd2.SetArgs([]string{"clear", "--all"})
		require.NoError(t, cmd2.ExecuteContext(t.Context()))
	})

	t.Run("login clear honors DIVERGE_SERVER over config.yaml", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")
		require.NoError(t, os.WriteFile(cfgPath, []byte("version: 1\nserver: https://prod.example.com\n"), 0o600))
		storePath := filepath.Join(dir, "credentials.json")
		require.NoError(t, SaveCredentialStore(storePath, &CredentialStore{
			Version: 1,
			Servers: map[string]ServerCredential{
				"https://prod.example.com":    {IDToken: "prod-tok"},
				"https://staging.example.com": {IDToken: "staging-tok"},
			},
		}))

		t.Setenv("DIVERGE_SERVER", "https://staging.example.com")
		var out bytes.Buffer
		cmd := newLoginCmdWithDeps(&App{}, &loginDeps{
			userConfigPath: cfgPath,
			credStorePath:  storePath,
		})
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"clear"})
		require.NoError(t, cmd.ExecuteContext(t.Context()))

		assert.Contains(t, out.String(), "https://staging.example.com")
		assert.False(t, HasStoredCredential(storePath, "https://staging.example.com"))
		assert.True(t, HasStoredCredential(storePath, "https://prod.example.com"))
	})
}
