package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/divergedev/diverge/internal/server/auth"
)

// OIDCHandlerConfig configures the OIDC authentication HTTP handlers.
type OIDCHandlerConfig struct {
	// IssuerURL is the OIDC provider's issuer URL.
	IssuerURL string
	// ClientID is the OIDC client ID.
	ClientID string
	// ClientSecret is the OIDC client secret.
	ClientSecret string
	// RedirectURL is the callback URL (e.g. https://diverge.example.com/auth/callback).
	RedirectURL string
	// Scopes are the OIDC scopes to request.
	Scopes []string
	// ProviderName is the display name shown on the login button (e.g. "Okta", "Google").
	ProviderName string
	// SessionManager mints and verifies session JWTs.
	SessionManager *auth.SessionManager
	// SessionMaxAge is the cookie max age. Defaults to 24 hours.
	SessionMaxAge time.Duration
	// SecureCookies sets the Secure flag on cookies. Should be true in production.
	SecureCookies bool
	// UsernameClaim is the JWT claim used for the username.
	UsernameClaim string
	// GroupsClaim is the JWT claim used for group membership.
	GroupsClaim string
	// Logger for request logging.
	Logger *slog.Logger
}

// OIDCHandler implements the OIDC authorization code flow endpoints.
type OIDCHandler struct {
	provider       *oidc.Provider
	oauth2Config   oauth2.Config
	verifier       *oidc.IDTokenVerifier
	sessionManager *auth.SessionManager
	sessionMaxAge  time.Duration
	secureCookies  bool
	usernameClaim  string
	groupsClaim    string
	providerName   string
	logger         *slog.Logger
}

// NewOIDCHandler creates a new OIDC handler. It performs OIDC discovery on startup.
func NewOIDCHandler(cfg OIDCHandlerConfig) (*OIDCHandler, error) {
	ctx := oidc.ClientContext(context.Background(), http.DefaultClient)
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery failed for %q: %w", cfg.IssuerURL, err)
	}

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email", "groups"}
	}

	sessionMaxAge := cfg.SessionMaxAge
	if sessionMaxAge == 0 {
		sessionMaxAge = 24 * time.Hour
	}

	usernameClaim := cfg.UsernameClaim
	if usernameClaim == "" {
		usernameClaim = "preferred_username"
	}
	groupsClaim := cfg.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = "groups"
	}

	providerName := cfg.ProviderName
	if providerName == "" {
		providerName = "SSO"
	}

	return &OIDCHandler{
		provider: provider,
		oauth2Config: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       scopes,
		},
		verifier:       provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		sessionManager: cfg.SessionManager,
		sessionMaxAge:  sessionMaxAge,
		secureCookies:  cfg.SecureCookies,
		usernameClaim:  usernameClaim,
		groupsClaim:    groupsClaim,
		providerName:   providerName,
		logger:         cfg.Logger,
	}, nil
}

// RegisterRoutes registers the OIDC auth endpoints on the given mux.
func (h *OIDCHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", h.HandleLogin)
	mux.HandleFunc("GET /auth/callback", h.HandleCallback)
	mux.HandleFunc("POST /auth/logout", h.HandleLogout)
	mux.HandleFunc("GET /auth/config", h.HandleConfig)
}

// HandleLogin initiates the OIDC authorization code flow.
func (h *OIDCHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	// Generate CSRF state
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		h.logger.Error("failed to generate state", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	// Store return URL in state cookie
	returnURL := r.URL.Query().Get("return_url")
	if returnURL == "" {
		returnURL = "/"
	}

	// Set state cookie (SameSite=Strict for CSRF protection)
	http.SetCookie(w, &http.Cookie{
		Name:     "diverge_oauthstate",
		Value:    state,
		Path:     "/auth/callback",
		MaxAge:   300, // 5 minutes
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})

	// Store return URL in a separate cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "diverge_returnurl",
		Value:    returnURL,
		Path:     "/auth/callback",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})

	// Redirect to OIDC provider
	authURL := h.oauth2Config.AuthCodeURL(state)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// HandleCallback processes the OIDC provider's authorization code callback.
func (h *OIDCHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	// Check for errors from the IdP
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		errDesc := r.URL.Query().Get("error_description")
		h.logger.Warn("oidc callback error", "error", errParam, "description", errDesc)
		h.renderCallbackError(w, r, fmt.Sprintf("%s: %s", errParam, errDesc))
		return
	}

	// Verify CSRF state
	stateCookie, err := r.Cookie("diverge_oauthstate")
	if err != nil {
		h.renderCallbackError(w, r, "Missing OAuth state cookie. Please try logging in again.")
		return
	}

	state := r.URL.Query().Get("state")
	if state == "" || state != stateCookie.Value {
		h.renderCallbackError(w, r, "Invalid OAuth state. This may be a CSRF attack. Please try again.")
		return
	}

	// Exchange authorization code for tokens
	code := r.URL.Query().Get("code")
	if code == "" {
		h.renderCallbackError(w, r, "Missing authorization code. Please try again.")
		return
	}

	oauth2Token, err := h.oauth2Config.Exchange(r.Context(), code)
	if err != nil {
		h.logger.Error("oidc code exchange failed", "error", err)
		h.renderCallbackError(w, r, fmt.Sprintf("Failed to exchange authorization code: %v", err))
		return
	}

	// Extract and verify ID token
	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		h.renderCallbackError(w, r, "No ID token in response from identity provider.")
		return
	}

	idToken, err := h.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		h.logger.Error("oidc id_token verification failed", "error", err)
		h.renderCallbackError(w, r, fmt.Sprintf("ID token verification failed: %v", err))
		return
	}

	// Extract claims
	var rawClaims map[string]interface{}
	if err := idToken.Claims(&rawClaims); err != nil {
		h.renderCallbackError(w, r, "Failed to parse identity token claims.")
		return
	}

	username := auth.ClaimString(rawClaims, h.usernameClaim)
	if username == "" {
		username = auth.ClaimString(rawClaims, "email")
	}
	if username == "" {
		username = idToken.Subject
	}
	email := auth.ClaimString(rawClaims, "email")
	groups := auth.ClaimStringSlice(rawClaims, h.groupsClaim)

	// Mint session JWT
	sessionToken, err := h.sessionManager.Mint(username, email, "oidc", groups)
	if err != nil {
		h.logger.Error("failed to mint session token", "error", err, "user", username)
		h.renderCallbackError(w, r, "Failed to create session. Please try again.")
		return
	}

	// Clear state cookies
	h.clearCookie(w, "diverge_oauthstate", "/auth/callback")
	h.clearCookie(w, "diverge_returnurl", "/auth/callback")

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "diverge_token",
		Value:    sessionToken,
		Path:     "/",
		MaxAge:   int(h.sessionMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})

	h.logger.Info("oidc login successful", "user", username, "email", email, "groups", groups)

	// Redirect to return URL
	returnURL := "/"
	if cookie, err := r.Cookie("diverge_returnurl"); err == nil && cookie.Value != "" {
		returnURL = cookie.Value
	}
	http.Redirect(w, r, returnURL, http.StatusFound)
}

// HandleLogout clears the session cookie and redirects to the login page.
func (h *OIDCHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	h.clearCookie(w, "diverge_token", "/")
	http.Redirect(w, r, "/login", http.StatusFound)
}

// authConfigResponse is returned by /auth/config for the frontend.
type authConfigResponse struct {
	OIDCEnabled  bool   `json:"oidcEnabled"`
	ProviderName string `json:"providerName,omitempty"`
	LoginURL     string `json:"loginUrl,omitempty"`
}

// HandleConfig returns the public auth configuration for the frontend.
func (h *OIDCHandler) HandleConfig(w http.ResponseWriter, r *http.Request) {
	resp := authConfigResponse{
		OIDCEnabled:  true,
		ProviderName: h.providerName,
		LoginURL:     "/auth/login",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// renderCallbackError shows an actionable error page on callback failures.
func (h *OIDCHandler) renderCallbackError(w http.ResponseWriter, r *http.Request, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Authentication Error — Diverge</title>
<style>
	body { font-family: system-ui, sans-serif; max-width: 600px; margin: 80px auto; padding: 0 20px; color: #e2e8f0; background: #0f172a; }
	.error-box { background: #1e293b; border: 1px solid #ef4444; border-radius: 8px; padding: 24px; margin: 20px 0; }
	.error-title { color: #ef4444; font-size: 1.2em; margin: 0 0 12px 0; }
	.error-msg { color: #94a3b8; line-height: 1.6; }
	a { color: #38bdf8; }
	.actions { margin-top: 20px; }
	.btn { display: inline-block; padding: 8px 16px; background: #3b82f6; color: white; text-decoration: none; border-radius: 6px; margin-right: 8px; }
	.btn-outline { background: transparent; border: 1px solid #475569; color: #e2e8f0; }
</style></head>
<body>
	<h1>🔒 Authentication Error</h1>
	<div class="error-box">
		<p class="error-title">Login Failed</p>
		<p class="error-msg">%s</p>
	</div>
	<div class="actions">
		<a href="/login" class="btn">Try Again</a>
		<a href="/docs/guides/sso-setup" class="btn btn-outline">SSO Setup Guide</a>
	</div>
</body>
</html>`, message)
}

func (h *OIDCHandler) clearCookie(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})
}
