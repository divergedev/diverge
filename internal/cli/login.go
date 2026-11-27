package cli

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gofrs/flock"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/divergedev/diverge/internal/server/auth"
	"github.com/divergedev/diverge/internal/tunnelclient"
)

const (
	credentialStoreVersion    = 1
	defaultLoginTimeout       = 5 * time.Minute
	defaultLoginCallbackPath  = "/auth/callback"
	defaultTokenExpiryLeeway  = 60 * time.Second
	loginHTTPClientTimeout    = 15 * time.Second
	loginShutdownGraceTimeout = 2 * time.Second
)

// CredentialStore persists OIDC tokens returned by `diverge login` per server URL.
type CredentialStore struct {
	Version int                         `json:"version"`
	Servers map[string]ServerCredential `json:"servers"`
}

// ServerCredential stores OIDC ID and refresh tokens for a single diverge-server instance.
type ServerCredential struct {
	IssuerURL      string    `json:"issuer_url"`
	ClientID       string    `json:"client_id"`
	Subject        string    `json:"subject,omitempty"`
	TokenURL       string    `json:"token_url"`
	Scopes         []string  `json:"scopes,omitempty"`
	IDToken        string    `json:"id_token"`
	PendingIDToken string    `json:"pending_id_token,omitempty"`
	AccessToken    string    `json:"access_token,omitempty"`
	RefreshToken   string    `json:"refresh_token,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	Username       string    `json:"username,omitempty"`
	Email          string    `json:"email,omitempty"`
}

type cliAuthConfig struct {
	OIDCEnabled  bool     `json:"oidcEnabled"`
	ProviderName string   `json:"providerName"`
	LoginURL     string   `json:"loginUrl"`
	IssuerURL    string   `json:"issuerUrl"`
	CLIClientID  string   `json:"cliClientId"`
	Scopes       []string `json:"scopes"`
}

type loginDeps struct {
	userConfigPath string
	credStorePath  string
	httpClient     *http.Client
	openBrowser    func(string) error
}

type loginOptions struct {
	Server       string
	Port         int
	CallbackPath string
	Scopes       string
	NoBrowser    bool
	Timeout      time.Duration
}

// CredentialsPath returns the path to $XDG_CONFIG_HOME/diverge/credentials.json
// (or ~/.config/diverge/credentials.json).
func CredentialsPath() (string, error) {
	cfgRoot := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if cfgRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("could not determine user home directory")
		}
		cfgRoot = filepath.Join(home, ".config")
	}
	return filepath.Join(cfgRoot, "diverge", "credentials.json"), nil
}

func normalizeServerKey(serverAddr string) string {
	return strings.TrimRight(strings.TrimSpace(serverAddr), "/")
}

// LoadCredentialStore loads credentials from path, returning an initialized empty
// store if the file does not exist.
func LoadCredentialStore(path string) (*CredentialStore, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- user-owned credentials path
	if err != nil {
		if os.IsNotExist(err) {
			return &CredentialStore{
				Version: credentialStoreVersion,
				Servers: make(map[string]ServerCredential),
			}, nil
		}
		return nil, fmt.Errorf("failed to read credentials %s: %w", path, err)
	}
	var store CredentialStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("failed to parse credentials %s: %w", path, err)
	}
	if store.Version != 0 && store.Version != credentialStoreVersion {
		return nil, fmt.Errorf("unsupported credentials version %d in %s", store.Version, path)
	}
	if store.Version == 0 {
		store.Version = credentialStoreVersion
	}
	if store.Servers == nil {
		store.Servers = make(map[string]ServerCredential)
	}
	return &store, nil
}

// SaveCredentialStore atomically writes store to path with mode 0600 (0700 parent dir).
func SaveCredentialStore(path string, store *CredentialStore) error {
	if store == nil {
		return errors.New("nil credential store")
	}
	if store.Version == 0 {
		store.Version = credentialStoreVersion
	}
	if store.Servers == nil {
		store.Servers = make(map[string]ServerCredential)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create credentials directory %s: %w", dir, err)
	}
	_ = os.Chmod(dir, 0o700)

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary credentials file: %w", err)
	}
	tmpName := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to chmod credentials file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write credentials file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to sync credentials file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close credentials file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil { // #nosec G703 -- path resolved under user config directory
		return fmt.Errorf("failed to persist credentials file %s: %w", path, err)
	}
	cleanupTmp = false
	return nil
}

func withCredentialStoreLock(ctx context.Context, storePath string, fn func() error) error {
	dir := filepath.Dir(storePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create credentials directory %s: %w", dir, err)
	}
	_ = os.Chmod(dir, 0o700)

	lock := flock.New(storePath + ".lock")
	locked, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		return fmt.Errorf("failed to lock credentials store %s: %w", storePath, err)
	}
	if !locked {
		return fmt.Errorf("could not acquire lock on credentials store %s", storePath)
	}
	defer func() { _ = lock.Unlock() }()
	return fn()
}

func hasStoredCredential(storePath, serverAddr string) (bool, error) {
	serverKey := normalizeServerKey(serverAddr)
	if storePath == "" || serverKey == "" {
		return false, nil
	}
	store, err := LoadCredentialStore(storePath)
	if err != nil {
		return false, err
	}
	cred, ok := store.Servers[serverKey]
	return ok && (strings.TrimSpace(cred.IDToken) != "" || strings.TrimSpace(cred.PendingIDToken) != "" || strings.TrimSpace(cred.RefreshToken) != ""), nil
}

// HasStoredCredential reports whether credentials.json holds an entry for serverAddr.
func HasStoredCredential(storePath, serverAddr string) bool {
	ok, err := hasStoredCredential(storePath, serverAddr)
	return err == nil && ok
}

type loopbackHTTPGuard struct {
	base http.RoundTripper
}

func (g *loopbackHTTPGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL != nil && strings.EqualFold(req.URL.Scheme, "http") && !tunnelclient.IsLoopbackHost(req.URL.Host) {
		return nil, fmt.Errorf("%w, got %q", tunnelclient.ErrInsecureHTTP, req.URL.Host)
	}
	base := g.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func wrapLoginHTTPClient(baseClient *http.Client) *http.Client {
	var baseRT http.RoundTripper
	timeout := loginHTTPClientTimeout
	if baseClient != nil {
		baseRT = baseClient.Transport
		if baseClient.Timeout > 0 {
			timeout = baseClient.Timeout
		}
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &loopbackHTTPGuard{
			base: baseRT,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if len(via) > 0 {
				orig := via[0]
				if !strings.EqualFold(req.URL.Scheme, orig.URL.Scheme) || !strings.EqualFold(req.URL.Host, orig.URL.Host) {
					return fmt.Errorf("refusing cross-origin redirect from %s to %s", orig.URL, req.URL)
				}
			}
			return nil
		},
	}
}

func validateSecureEndpointURL(raw, fieldName string) (string, error) {
	// Do NOT strip trailing slashes: OIDC Discovery requires exact issuer string equality.
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%s is empty", fieldName)
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] < 0x20 || trimmed[i] == 0x7f {
			return "", fmt.Errorf("invalid %s %q", fieldName, raw)
		}
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid %s %q", fieldName, raw)
	}
	if strings.EqualFold(u.Scheme, "https") {
		return trimmed, nil
	}
	if strings.EqualFold(u.Scheme, "http") && tunnelclient.IsLoopbackHost(u.Host) {
		return trimmed, nil
	}
	return "", fmt.Errorf("%w: %s must use https:// (cleartext http:// is only allowed for loopback hosts): %s", tunnelclient.ErrInsecureHTTP, fieldName, raw)
}

func validateCallbackPath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return defaultLoginCallbackPath, nil
	}
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("invalid --callback-path %q: must start with '/'", raw)
	}
	if strings.Contains(p, "//") || strings.ContainsAny(p, " \t\r\n?#{}") {
		return "", fmt.Errorf("invalid --callback-path %q: must be a clean URL path without query, fragment, wildcards, or whitespace", raw)
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] == 0x7f {
			return "", fmt.Errorf("invalid --callback-path %q: contains control character", raw)
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return "", fmt.Errorf("invalid --callback-path %q: must not contain dot segments", raw)
		}
	}
	u, err := url.ParseRequestURI(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || u.Path != p {
		return "", fmt.Errorf("invalid --callback-path %q: must be a clean URL path", raw)
	}
	return p, nil
}

// StoredCredentialTokenSource resolves and automatically refreshes OIDC ID tokens
// stored by `diverge login` in credentials.json.
type StoredCredentialTokenSource struct {
	storePath      string
	serverKey      string
	httpClient     *http.Client
	leeway         time.Duration
	mu             sync.Mutex
	cached         *ServerCredential
	forcedRefresh  bool
	invalidatedTok string
}

// NewStoredCredentialTokenSource returns an auto-refreshing TokenSource backed by credentials.json.
func NewStoredCredentialTokenSource(storePath, serverAddr string, httpClient *http.Client) *StoredCredentialTokenSource {
	return &StoredCredentialTokenSource{
		storePath:  storePath,
		serverKey:  normalizeServerKey(serverAddr),
		httpClient: wrapLoginHTTPClient(httpClient),
		leeway:     defaultTokenExpiryLeeway,
	}
}

// Invalidate marks the currently cached credential stale so the next Token call
// forces a reload or OAuth2 refresh_token exchange.
func (s *StoredCredentialTokenSource) Invalidate() {
	s.mu.Lock()
	s.forcedRefresh = true
	if s.cached != nil {
		s.invalidatedTok = s.cached.IDToken
	}
	s.mu.Unlock()
}

func isCredentialUsable(now, expiresAt time.Time, hasRefreshToken bool, leeway time.Duration) bool {
	if hasRefreshToken {
		return now.Add(leeway).Before(expiresAt)
	}
	return now.Before(expiresAt)
}

// Token returns a valid OIDC ID token, refreshing via refresh_token and persisting
// rotated tokens cleanly when expired or invalidated.
func (s *StoredCredentialTokenSource) Token(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if !s.forcedRefresh && s.cached != nil && s.cached.IDToken != "" &&
		isCredentialUsable(now, s.cached.ExpiresAt, strings.TrimSpace(s.cached.RefreshToken) != "", s.leeway) {
		return s.cached.IDToken, nil
	}

	var resultTok string
	err := withCredentialStoreLock(ctx, s.storePath, func() error {
		now = time.Now()
		store, err := LoadCredentialStore(s.storePath)
		if err != nil {
			return err
		}
		cred, ok := store.Servers[s.serverKey]
		if !ok {
			return fmt.Errorf("%w: %w for %s; run 'diverge login --server %s'",
				tunnelclient.ErrAuthRequired, ErrNoTunnelCredential, s.serverKey, s.serverKey)
		}

		hasRefresh := strings.TrimSpace(cred.RefreshToken) != ""
		if cred.IDToken != "" && isCredentialUsable(now, cred.ExpiresAt, hasRefresh, s.leeway) &&
			(!s.forcedRefresh || cred.IDToken != s.invalidatedTok) {
			cp := cred
			s.cached = &cp
			s.forcedRefresh = false
			s.invalidatedTok = ""
			resultTok = cred.IDToken
			return nil
		}

		pending := strings.TrimSpace(cred.PendingIDToken)
		if pending == "" && !hasRefresh {
			return fmt.Errorf("%w: stored OIDC token for %s is expired or invalid and has no refresh token; run 'diverge login --server %s'",
				tunnelclient.ErrAuthRequired, s.serverKey, s.serverKey)
		}
		if _, err := validateSecureEndpointURL(cred.IssuerURL, "issuer_url"); err != nil {
			return err
		}
		if hasRefresh {
			if _, err := validateSecureEndpointURL(cred.TokenURL, "token_url"); err != nil {
				return err
			}
		}

		oauthCtx := context.WithValue(ctx, oauth2.HTTPClient, s.httpClient)
		oidcCtx := oidc.ClientContext(oauthCtx, s.httpClient)

		// Run OIDC discovery before exchanging a refresh token so transient discovery
		// failures never consume or lose a rotating refresh token.
		provider, err := oidc.NewProvider(oidcCtx, cred.IssuerURL)
		if err != nil {
			return fmt.Errorf("failed OIDC discovery during refresh for %s: %w", cred.IssuerURL, err)
		}
		verifier := provider.Verifier(&oidc.Config{ClientID: cred.ClientID})

		if pending != "" {
			tok, handled, err := s.verifyPendingIDToken(oidcCtx, verifier, store, &cred, now)
			if handled {
				if err != nil {
					return err
				}
				resultTok = tok
				return nil
			}
			hasRefresh = strings.TrimSpace(cred.RefreshToken) != ""
		}

		if !hasRefresh {
			return fmt.Errorf("%w: stored OIDC token for %s is expired or invalid and has no refresh token; run 'diverge login --server %s'",
				tunnelclient.ErrAuthRequired, s.serverKey, s.serverKey)
		}

		tok, err := s.exchangeAndVerifyRefreshToken(oauthCtx, oidcCtx, verifier, store, cred)
		if err != nil {
			return err
		}
		resultTok = tok
		return nil
	})
	if err != nil {
		return "", err
	}
	return resultTok, nil
}

func (s *StoredCredentialTokenSource) verifyPendingIDToken(
	oidcCtx context.Context,
	verifier *oidc.IDTokenVerifier,
	store *CredentialStore,
	cred *ServerCredential,
	now time.Time,
) (string, bool, error) {
	pending := strings.TrimSpace(cred.PendingIDToken)
	if pending == "" {
		return "", false, nil
	}

	idToken, err := verifier.Verify(oidcCtx, pending)
	if err != nil {
		var expiredErr *oidc.TokenExpiredError
		if errors.As(err, &expiredErr) {
			cred.PendingIDToken = ""
			store.Servers[s.serverKey] = *cred
			if saveErr := SaveCredentialStore(s.storePath, store); saveErr != nil {
				return "", true, saveErr
			}
			if strings.TrimSpace(cred.RefreshToken) != "" {
				return "", false, nil
			}
			return "", true, fmt.Errorf("%w: stored OIDC token for %s has expired and has no refresh token; run 'diverge login --server %s'",
				tunnelclient.ErrAuthRequired, s.serverKey, s.serverKey)
		}
		return "", true, fmt.Errorf("refreshed ID token verification failed: %w", err)
	}

	if cred.Subject != "" && idToken.Subject != cred.Subject {
		subErr := fmt.Errorf("%w: refreshed ID token subject %q does not match stored subject %q; run 'diverge login --server %s'",
			tunnelclient.ErrAuthRequired, idToken.Subject, cred.Subject, s.serverKey)
		cred.PendingIDToken = ""
		store.Servers[s.serverKey] = *cred
		if saveErr := SaveCredentialStore(s.storePath, store); saveErr != nil {
			return "", true, errors.Join(subErr, saveErr)
		}
		return "", true, subErr
	}

	hasRefresh := strings.TrimSpace(cred.RefreshToken) != ""
	if (!s.forcedRefresh || pending != s.invalidatedTok) &&
		isCredentialUsable(now, idToken.Expiry, hasRefresh, s.leeway) {
		tok, err := s.promoteVerifiedIDToken(store, *cred, pending, idToken)
		return tok, true, err
	}

	cred.PendingIDToken = ""
	store.Servers[s.serverKey] = *cred
	if saveErr := SaveCredentialStore(s.storePath, store); saveErr != nil {
		return "", true, saveErr
	}
	return "", false, nil
}

func (s *StoredCredentialTokenSource) exchangeAndVerifyRefreshToken(
	oauthCtx context.Context,
	oidcCtx context.Context,
	verifier *oidc.IDTokenVerifier,
	store *CredentialStore,
	cred ServerCredential,
) (string, error) {
	oauthCfg := oauth2.Config{
		ClientID: cred.ClientID,
		Endpoint: oauth2.Endpoint{
			TokenURL:  cred.TokenURL,
			AuthStyle: oauth2.AuthStyleInParams,
		},
		Scopes: cred.Scopes,
	}
	tok, err := oauthCfg.TokenSource(oauthCtx, &oauth2.Token{
		RefreshToken: cred.RefreshToken,
	}).Token()
	if err != nil {
		var retrieveErr *oauth2.RetrieveError
		if errors.As(err, &retrieveErr) {
			status := 0
			if retrieveErr.Response != nil {
				status = retrieveErr.Response.StatusCode
			}
			if status == http.StatusBadRequest || status == http.StatusUnauthorized || status == http.StatusForbidden ||
				retrieveErr.ErrorCode == "invalid_grant" || retrieveErr.ErrorCode == "invalid_client" || retrieveErr.ErrorCode == "unauthorized_client" {
				return "", fmt.Errorf("%w: failed to refresh OIDC token for %s (run 'diverge login --server %s'): %w",
					tunnelclient.ErrAuthRequired, s.serverKey, s.serverKey, err)
			}
		}
		return "", fmt.Errorf("failed to refresh OIDC token for %s (run 'diverge login --server %s'): %w",
			s.serverKey, s.serverKey, err)
	}

	rotatedGrant := false
	if tok.AccessToken != "" && tok.AccessToken != cred.AccessToken {
		cred.AccessToken = tok.AccessToken
		rotatedGrant = true
	}
	if tok.RefreshToken != "" && tok.RefreshToken != cred.RefreshToken {
		cred.RefreshToken = tok.RefreshToken
		rotatedGrant = true
	}

	rawIDToken, _ := tok.Extra("id_token").(string)
	rawIDToken = strings.TrimSpace(rawIDToken)
	if rawIDToken == "" {
		authErr := fmt.Errorf("%w: OIDC refresh for %s did not return an id_token; run 'diverge login --server %s'",
			tunnelclient.ErrAuthRequired, s.serverKey, s.serverKey)
		if rotatedGrant {
			cred.PendingIDToken = ""
			store.Servers[s.serverKey] = cred
			if saveErr := SaveCredentialStore(s.storePath, store); saveErr != nil {
				return "", errors.Join(authErr, saveErr)
			}
		}
		return "", authErr
	}

	idToken, err := verifier.Verify(oidcCtx, rawIDToken)
	if err != nil {
		verifyErr := fmt.Errorf("refreshed ID token verification failed: %w", err)
		var expiredErr *oidc.TokenExpiredError
		if errors.As(err, &expiredErr) {
			cred.PendingIDToken = ""
		} else {
			// Persist the unverified rotated ID token separately so a subsequent retry can finish
			// JWKS verification without replaying the consumed refresh token, and without exposing
			// unverified claims via IDToken.
			cred.PendingIDToken = rawIDToken
		}
		if rotatedGrant || cred.PendingIDToken != "" {
			store.Servers[s.serverKey] = cred
			if saveErr := SaveCredentialStore(s.storePath, store); saveErr != nil {
				return "", errors.Join(verifyErr, saveErr)
			}
		}
		return "", verifyErr
	}

	return s.promoteVerifiedIDToken(store, cred, rawIDToken, idToken)
}

func (s *StoredCredentialTokenSource) promoteVerifiedIDToken(
	store *CredentialStore,
	cred ServerCredential,
	rawIDToken string,
	idToken *oidc.IDToken,
) (string, error) {
	if cred.Subject != "" && idToken.Subject != cred.Subject {
		subErr := fmt.Errorf("%w: refreshed ID token subject %q does not match stored subject %q; run 'diverge login --server %s'",
			tunnelclient.ErrAuthRequired, idToken.Subject, cred.Subject, s.serverKey)
		if cred.PendingIDToken != "" {
			cred.PendingIDToken = ""
			store.Servers[s.serverKey] = cred
			if saveErr := SaveCredentialStore(s.storePath, store); saveErr != nil {
				return "", errors.Join(subErr, saveErr)
			}
		}
		return "", subErr
	}
	if cred.Subject == "" {
		cred.Subject = idToken.Subject
	}

	cred.IDToken = rawIDToken
	cred.PendingIDToken = ""
	cred.ExpiresAt = idToken.Expiry
	if user, email := extractOIDCIdentity(idToken); user != "" || email != "" {
		if user != "" {
			cred.Username = user
		}
		if email != "" {
			cred.Email = email
		}
	}

	store.Servers[s.serverKey] = cred
	if saveErr := SaveCredentialStore(s.storePath, store); saveErr != nil {
		return "", saveErr
	}

	cp := cred
	s.cached = &cp
	s.forcedRefresh = false
	s.invalidatedTok = ""
	return rawIDToken, nil
}

func newLoginCmd(app *App) *cobra.Command {
	return newLoginCmdWithDeps(app, nil)
}

func newLoginCmdWithDeps(_ *App, deps *loginDeps) *cobra.Command {
	opts := loginOptions{
		CallbackPath: defaultLoginCallbackPath,
		Timeout:      defaultLoginTimeout,
	}

	cmd := &cobra.Command{
		Use:          "login",
		Short:        "Authenticate with the Diverge server via OIDC PKCE without kubeconfig",
		SilenceUsage: true,
		Long: `Authenticate directly against your organization's OIDC identity provider
using RFC 8252 Authorization Code + PKCE (S256) and store credentials locally in
~/.config/diverge/credentials.json (mode 0600).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLogin(cmd.Context(), cmd, opts, deps)
		},
	}

	cmd.Flags().StringVar(&opts.Server, "server", "", "Diverge server URL (default: $DIVERGE_SERVER or ~/.config/diverge/config.yaml)")
	cmd.Flags().IntVar(&opts.Port, "port", 0, "Localhost loopback callback port (default: ephemeral free port)")
	cmd.Flags().StringVar(&opts.CallbackPath, "callback-path", defaultLoginCallbackPath, "Localhost loopback callback path")
	cmd.Flags().StringVar(&opts.Scopes, "scopes", "", "Comma-separated OIDC scopes override (default: server /auth/config scopes)")
	cmd.Flags().BoolVar(&opts.NoBrowser, "no-browser", false, "Print login URL without launching a web browser")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", defaultLoginTimeout, "Maximum time to wait for browser login completion")

	cmd.AddCommand(newLoginStatusCmdWithDeps(deps))
	cmd.AddCommand(newLoginClearCmdWithDeps(deps))
	return cmd
}

func newLoginStatusCmdWithDeps(deps *loginDeps) *cobra.Command {
	var serverFlag string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:          "status",
		Short:        "Show stored OIDC login credentials",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLoginStatus(cmd, serverFlag, jsonOut, deps)
		},
	}
	cmd.Flags().StringVar(&serverFlag, "server", "", "Filter by Diverge server URL")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output credential status as JSON")
	return cmd
}

func newLoginClearCmdWithDeps(deps *loginDeps) *cobra.Command {
	var serverFlag string
	var clearAll bool
	cmd := &cobra.Command{
		Use:          "clear",
		Short:        "Remove stored OIDC credentials from credentials.json",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLoginClear(cmd, serverFlag, clearAll, deps)
		},
	}
	cmd.Flags().StringVar(&serverFlag, "server", "", "Diverge server URL to remove")
	cmd.Flags().BoolVar(&clearAll, "all", false, "Remove stored credentials for all servers")
	return cmd
}

func resolveCredStorePath(deps *loginDeps) (string, error) {
	if deps != nil && deps.credStorePath != "" {
		return deps.credStorePath, nil
	}
	return CredentialsPath()
}

func loadLoginUserConfig(deps *loginDeps) (*UserConfig, error) {
	if deps != nil && deps.userConfigPath != "" {
		return LoadUserConfigFrom(deps.userConfigPath)
	}
	return LoadUserConfig()
}

func randomURLToken(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func extractOIDCIdentity(idToken *oidc.IDToken) (username, email string) {
	var rawClaims map[string]any
	if err := idToken.Claims(&rawClaims); err == nil {
		username = auth.ClaimString(rawClaims, "preferred_username")
		email = auth.ClaimString(rawClaims, "email")
	}
	if username == "" {
		username = email
	}
	if username == "" {
		username = idToken.Subject
	}
	return username, email
}

func runLogin(ctx context.Context, cmd *cobra.Command, opts loginOptions, deps *loginDeps) error {
	if deps == nil {
		deps = &loginDeps{}
	}
	if opts.Port < 0 || opts.Port > 65535 {
		return fmt.Errorf("--port must be between 0 and 65535, got %d", opts.Port)
	}
	if opts.Timeout <= 0 {
		return fmt.Errorf("--timeout must be positive, got %s", opts.Timeout)
	}
	callbackPath, err := validateCallbackPath(opts.CallbackPath)
	if err != nil {
		return err
	}

	userCfg, err := loadLoginUserConfig(deps)
	if err != nil {
		return err
	}
	serverAddr, err := resolveServerAddr(opts.Server, userCfg)
	if err != nil {
		return err
	}
	serverAddr, err = validateSecureEndpointURL(serverAddr, "server URL")
	if err != nil {
		return err
	}

	storePath, err := resolveCredStorePath(deps)
	if err != nil {
		return err
	}

	hc := wrapLoginHTTPClient(deps.httpClient)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverAddr+"/auth/config", nil)
	if err != nil {
		return fmt.Errorf("failed to build /auth/config request: %w", err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch %s/auth/config: %w", serverAddr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("server %s returned HTTP %d from /auth/config (OIDC authentication may not be enabled on this server)", serverAddr, resp.StatusCode)
		}
		return fmt.Errorf("server %s returned HTTP %d from /auth/config", serverAddr, resp.StatusCode)
	}

	var cfg cliAuthConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return fmt.Errorf("failed to parse /auth/config response: %w", err)
	}
	if !cfg.OIDCEnabled {
		return fmt.Errorf("server %s reports OIDC authentication is disabled", serverAddr)
	}
	issuerURL, err := validateSecureEndpointURL(cfg.IssuerURL, "issuerUrl")
	if err != nil {
		return err
	}
	cliClientID := strings.TrimSpace(cfg.CLIClientID)
	if cliClientID == "" {
		return fmt.Errorf("server %s returned empty cliClientId in /auth/config", serverAddr)
	}

	scopes := parseScopesOverride(opts.Scopes, cfg.Scopes)

	oauthCtx := context.WithValue(ctx, oauth2.HTTPClient, hc)
	oidcCtx := oidc.ClientContext(oauthCtx, hc)
	provider, err := oidc.NewProvider(oidcCtx, issuerURL)
	if err != nil {
		return fmt.Errorf("OIDC discovery failed for %s: %w", issuerURL, err)
	}
	ep := provider.Endpoint()
	if _, err := validateSecureEndpointURL(ep.AuthURL, "authorization_endpoint"); err != nil {
		return err
	}
	if _, err := validateSecureEndpointURL(ep.TokenURL, "token_endpoint"); err != nil {
		return err
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", opts.Port)) // #nosec G102 -- loopback only per RFC 8252
	if err != nil {
		return fmt.Errorf("failed to bind loopback callback listener on 127.0.0.1:%d: %w", opts.Port, err)
	}
	redirectURI := fmt.Sprintf("http://%s%s", ln.Addr().String(), callbackPath)

	pkceVerifier := oauth2.GenerateVerifier()
	state, err := randomURLToken(32)
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("failed to generate OAuth state: %w", err)
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("failed to generate OIDC nonce: %w", err)
	}

	ep.AuthStyle = oauth2.AuthStyleInParams
	oauthCfg := oauth2.Config{
		ClientID:    cliClientID,
		Endpoint:    ep,
		RedirectURL: redirectURI,
		Scopes:      scopes,
	}

	type callbackResult struct {
		code string
		err  error
	}
	resultCh := make(chan callbackResult, 1)
	var deliverOnce sync.Once
	deliver := func(res callbackResult) {
		deliverOnce.Do(func() {
			resultCh <- res
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+callbackPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")

		// Verify CSRF state first so unauthenticated tabs cannot inject error text or abort login.
		gotState := r.URL.Query().Get("state")
		if subtle.ConstantTimeCompare([]byte(gotState), []byte(state)) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, "<html><body><h1>Invalid State</h1><p>State mismatch; please retry login.</p></body></html>")
			return
		}

		if errParam := r.URL.Query().Get("error"); errParam != "" {
			errDesc := r.URL.Query().Get("error_description")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, "<html><body><h1>Login Failed</h1><p>%s: %s</p></body></html>",
				html.EscapeString(errParam), html.EscapeString(errDesc))
			deliver(callbackResult{err: fmt.Errorf("OIDC provider returned error %s: %s", errParam, errDesc)})
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, "<html><body><h1>Missing Code</h1><p>No authorization code in callback.</p></body></html>")
			deliver(callbackResult{err: errors.New("callback missing authorization code")})
			return
		}

		_, _ = fmt.Fprint(w, "<html><body><h1>Callback Received</h1><p>Diverge CLI received the login callback. You may close this tab and check your terminal for the result.</p></body></html>")
		deliver(callbackResult{code: code})
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		_ = srv.Serve(ln)
	}()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), loginShutdownGraceTimeout)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	authOpts := []oauth2.AuthCodeOption{
		oauth2.S256ChallengeOption(pkceVerifier),
		oidc.Nonce(nonce),
	}
	if slices.Contains(scopes, oidc.ScopeOfflineAccess) {
		authOpts = append(authOpts, oauth2.SetAuthURLParam("prompt", "consent"))
	}
	authURL := oauthCfg.AuthCodeURL(state, authOpts...)
	if _, err := validateSecureEndpointURL(authURL, "authorization_url"); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if cfg.ProviderName != "" {
		_, _ = fmt.Fprintf(out, "Opening browser to sign in with %s...\n", cfg.ProviderName)
	} else {
		_, _ = fmt.Fprintln(out, "Opening browser to sign in...")
	}
	_, _ = fmt.Fprintf(out, "Authorize URL:\n%s\n\n", authURL)

	if !opts.NoBrowser {
		browserFn := openBrowser
		if deps.openBrowser != nil {
			browserFn = deps.openBrowser
		}
		if err := browserFn(authURL); err != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "⚠️  Could not open browser automatically (%v); open the URL above manually.\n", err)
		}
	}

	var code string
	timer := time.NewTimer(opts.Timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("timed out waiting for SSO login callback after %s", opts.Timeout)
	case res := <-resultCh:
		if res.err != nil {
			return res.err
		}
		code = res.code
	}

	oauthTok, err := oauthCfg.Exchange(oidcCtx, code, oauth2.VerifierOption(pkceVerifier))
	if err != nil {
		return fmt.Errorf("failed to exchange authorization code: %w", err)
	}

	rawIDToken, _ := oauthTok.Extra("id_token").(string)
	rawIDToken = strings.TrimSpace(rawIDToken)
	if rawIDToken == "" {
		return errors.New("OIDC token response missing id_token (ensure 'openid' scope is enabled)")
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: cliClientID})
	idToken, err := verifier.Verify(oidcCtx, rawIDToken)
	if err != nil {
		return fmt.Errorf("ID token verification failed: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 {
		return errors.New("ID token nonce mismatch")
	}

	username, email := extractOIDCIdentity(idToken)
	if err := withCredentialStoreLock(ctx, storePath, func() error {
		store, err := LoadCredentialStore(storePath)
		if err != nil {
			return err
		}
		store.Servers[normalizeServerKey(serverAddr)] = ServerCredential{
			IssuerURL:    issuerURL,
			ClientID:     cliClientID,
			Subject:      idToken.Subject,
			TokenURL:     provider.Endpoint().TokenURL,
			Scopes:       scopes,
			IDToken:      rawIDToken,
			AccessToken:  oauthTok.AccessToken,
			RefreshToken: oauthTok.RefreshToken,
			ExpiresAt:    idToken.Expiry,
			Username:     username,
			Email:        email,
		}
		return SaveCredentialStore(storePath, store)
	}); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "✅ Logged in to %s as %s (expires %s)\n",
		serverAddr, username, idToken.Expiry.UTC().Format(time.RFC3339))
	return nil
}

func parseScopesOverride(override string, fallback []string) []string {
	if strings.TrimSpace(override) != "" {
		var out []string
		for _, part := range strings.Split(override, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if len(fallback) > 0 {
		out := make([]string, 0, len(fallback))
		for _, s := range fallback {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{oidc.ScopeOpenID, "profile", "email", "groups"}
}

type loginStatusEntry struct {
	Server      string    `json:"server"`
	Username    string    `json:"username,omitempty"`
	Email       string    `json:"email,omitempty"`
	ClientID    string    `json:"client_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	Expired     bool      `json:"expired"`
	Refreshable bool      `json:"refreshable"`
}

func runLoginStatus(cmd *cobra.Command, serverFilter string, jsonOut bool, deps *loginDeps) error {
	storePath, err := resolveCredStorePath(deps)
	if err != nil {
		return err
	}
	store, err := LoadCredentialStore(storePath)
	if err != nil {
		return err
	}

	filterKey := normalizeServerKey(serverFilter)
	keys := make([]string, 0, len(store.Servers))
	for k := range store.Servers {
		if filterKey != "" && k != filterKey {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	now := time.Now()
	entries := make([]loginStatusEntry, 0, len(keys))
	for _, k := range keys {
		cred := store.Servers[k]
		entries = append(entries, loginStatusEntry{
			Server:      k,
			Username:    cred.Username,
			Email:       cred.Email,
			ClientID:    cred.ClientID,
			ExpiresAt:   cred.ExpiresAt,
			Expired:     !cred.ExpiresAt.IsZero() && now.After(cred.ExpiresAt),
			Refreshable: cred.RefreshToken != "",
		})
	}

	out := cmd.OutOrStdout()
	if jsonOut {
		return json.NewEncoder(out).Encode(entries)
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(out, "No stored credentials found. Run 'diverge login --server <url>' to sign in.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(w, "SERVER\tUSER\tEXPIRES\tEXPIRED\tREFRESHABLE")
	for _, e := range entries {
		user := e.Username
		if user == "" {
			user = e.Email
		}
		if user == "" {
			user = "-"
		}
		exp := "-"
		if !e.ExpiresAt.IsZero() {
			exp = e.ExpiresAt.UTC().Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%t\n", e.Server, user, exp, e.Expired, e.Refreshable)
	}
	return w.Flush()
}

func runLoginClear(cmd *cobra.Command, serverFlag string, clearAll bool, deps *loginDeps) error {
	storePath, err := resolveCredStorePath(deps)
	if err != nil {
		return err
	}

	ctx := context.Background()
	if cmd != nil && cmd.Context() != nil {
		ctx = cmd.Context()
	}

	out := cmd.OutOrStdout()
	if clearAll {
		if err := withCredentialStoreLock(ctx, storePath, func() error {
			if err := os.Remove(storePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("failed to remove credentials %s: %w", storePath, err)
			}
			return nil
		}); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "Cleared all stored credentials.")
		return nil
	}

	serverKey := normalizeServerKey(serverFlag)
	if serverKey == "" {
		serverKey = normalizeServerKey(os.Getenv("DIVERGE_SERVER"))
	}
	if serverKey == "" {
		userCfg, cfgErr := loadLoginUserConfig(deps)
		if cfgErr == nil && userCfg != nil {
			serverKey = normalizeServerKey(userCfg.Server)
		}
	}
	if serverKey == "" {
		return errors.New("specify --server <url> or --all")
	}

	var cleared bool
	if err := withCredentialStoreLock(ctx, storePath, func() error {
		store, err := LoadCredentialStore(storePath)
		if err != nil {
			return err
		}
		if _, ok := store.Servers[serverKey]; !ok {
			return nil
		}
		delete(store.Servers, serverKey)
		cleared = true
		return SaveCredentialStore(storePath, store)
	}); err != nil {
		return err
	}
	if !cleared {
		_, _ = fmt.Fprintf(out, "No stored credentials for %s.\n", serverKey)
		return nil
	}
	_, _ = fmt.Fprintf(out, "Cleared stored credentials for %s.\n", serverKey)
	return nil
}
