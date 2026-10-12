package server

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"hegel.dev/go/hegel"

	"github.com/divergedev/diverge/internal/server/auth"
)

func TestOIDCHandler_HandleConfig(t *testing.T) {
	// Create a minimal session manager for testing
	sm, err := auth.NewSessionManager(auth.SessionConfig{})
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	// We can't test full OIDC handler creation (requires real OIDC discovery),
	// but we can test the config endpoint and logout handler directly.
	handler := &OIDCHandler{
		providerName: "TestProvider",
	}

	// Test HandleConfig
	req := httptest.NewRequest("GET", "/auth/config", nil)
	rec := httptest.NewRecorder()
	handler.HandleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("HandleConfig status = %d, want %d", rec.Code, http.StatusOK)
	}

	var config authConfigResponse
	if err := json.NewDecoder(rec.Body).Decode(&config); err != nil {
		t.Fatalf("failed to decode config response: %v", err)
	}
	if !config.OIDCEnabled {
		t.Error("OIDCEnabled = false, want true")
	}
	if config.ProviderName != "TestProvider" {
		t.Errorf("ProviderName = %q, want %q", config.ProviderName, "TestProvider")
	}

	// Test HandleLogout
	handler.secureCookies = false
	req = httptest.NewRequest("POST", "/auth/logout", nil)
	rec = httptest.NewRecorder()
	handler.HandleLogout(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("HandleLogout status = %d, want %d", rec.Code, http.StatusFound)
	}

	location := rec.Header().Get("Location")
	if location != "/login" {
		t.Errorf("HandleLogout redirect = %q, want %q", location, "/login")
	}

	// Verify session cookie is cleared
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "diverge_token" {
			found = true
			if c.MaxAge != -1 {
				t.Errorf("diverge_token MaxAge = %d, want -1", c.MaxAge)
			}
		}
	}
	if !found {
		t.Error("diverge_token cookie not set in logout response")
	}

	// Suppress unused variable
	_ = sm
}

func TestOIDCHandler_HandleLogin_CSRF(t *testing.T) {
	handler := &OIDCHandler{
		secureCookies: false,
	}

	req := httptest.NewRequest("GET", "/auth/login?return_url=/dashboard", nil)
	rec := httptest.NewRecorder()
	handler.HandleLogin(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("HandleLogin status = %d, want %d", rec.Code, http.StatusFound)
	}

	// Verify state cookie is set
	cookies := rec.Result().Cookies()
	var stateCookie, returnCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "diverge_oauthstate" {
			stateCookie = c
		}
		if c.Name == "diverge_returnurl" {
			returnCookie = c
		}
	}

	if stateCookie == nil {
		t.Fatal("diverge_oauthstate cookie not set")
	}
	if stateCookie.HttpOnly != true {
		t.Error("diverge_oauthstate HttpOnly = false, want true")
	}
	if stateCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("diverge_oauthstate SameSite = %d, want Strict", stateCookie.SameSite)
	}

	if returnCookie == nil {
		t.Fatal("diverge_returnurl cookie not set")
	}
	if returnCookie.Value != "/dashboard" {
		t.Errorf("diverge_returnurl = %q, want %q", returnCookie.Value, "/dashboard")
	}
}

func TestOIDCHandler_HandleCallback_MissingState(t *testing.T) {
	handler := &OIDCHandler{
		secureCookies: false,
	}

	req := httptest.NewRequest("GET", "/auth/callback?code=abc", nil)
	rec := httptest.NewRecorder()
	handler.HandleCallback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "Missing OAuth state") {
		t.Errorf("body should mention missing state, got: %s", rec.Body.String())
	}
}

func TestOIDCHandler_HandleCallback_StateMismatch(t *testing.T) {
	handler := &OIDCHandler{
		secureCookies: false,
	}

	req := httptest.NewRequest("GET", "/auth/callback?code=abc&state=badstate", nil)
	req.AddCookie(&http.Cookie{Name: "diverge_oauthstate", Value: "goodstate"})
	rec := httptest.NewRecorder()
	handler.HandleCallback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "CSRF") {
		t.Errorf("body should mention CSRF, got: %s", rec.Body.String())
	}
}

func TestOIDCHandler_HandleCallback_IdPError(t *testing.T) {
	handler := &OIDCHandler{
		secureCookies: false,
		logger:        slog.Default(),
	}

	req := httptest.NewRequest("GET", "/auth/callback?error=access_denied&error_description=user+denied+consent", nil)
	rec := httptest.NewRecorder()
	handler.HandleCallback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "access_denied") {
		t.Errorf("body should contain error type, got: %s", body)
	}
}

func TestOIDCHandler_RenderCallbackError(t *testing.T) {
	handler := &OIDCHandler{}

	rec := httptest.NewRecorder()
	handler.renderCallbackError(rec, http.StatusBadRequest, "test error message")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "test error message") {
		t.Errorf("body should contain error message, got: %s", body)
	}
	if !strings.Contains(body, "Try Again") {
		t.Error("body should contain Try Again link")
	}
	if !strings.Contains(body, "SSO Setup Guide") {
		t.Error("body should contain SSO Setup Guide link")
	}
}

func TestOIDCHandler_RenderCallbackError_EscapesHTML(t *testing.T) {
	handler := &OIDCHandler{}

	rec := httptest.NewRecorder()
	xssPayload := `<script>alert("xss")</script>`
	handler.renderCallbackError(rec, http.StatusBadRequest, xssPayload)

	body := rec.Body.String()
	if strings.Contains(body, xssPayload) {
		t.Fatalf("expected XSS payload to be HTML-escaped, got raw payload in body: %s", body)
	}
	if !strings.Contains(body, `&lt;script&gt;alert(&#34;xss&#34;)&lt;/script&gt;`) {
		t.Errorf("expected escaped HTML entity in body, got: %s", body)
	}
}

func TestOIDCHandler_OpenRedirectPrevention(t *testing.T) {
	handler := &OIDCHandler{
		secureCookies: false,
	}

	maliciousURLs := []string{
		"https://evil.example.com/phish",
		"//evil.example.com/phish",
		`/\evil.example.com/phish`,
		"javascript:alert(1)",
	}

	for _, rawURL := range maliciousURLs {
		if got := sanitizeReturnURL(rawURL); got != "/" {
			t.Errorf("sanitizeReturnURL(%q) = %q, want \"/\"", rawURL, got)
		}

		req := httptest.NewRequest("GET", "/auth/login", nil)
		q := req.URL.Query()
		q.Set("return_url", rawURL)
		req.URL.RawQuery = q.Encode()

		rec := httptest.NewRecorder()
		handler.HandleLogin(rec, req)

		var returnCookie *http.Cookie
		for _, c := range rec.Result().Cookies() {
			if c.Name == "diverge_returnurl" {
				returnCookie = c
			}
		}
		if returnCookie == nil {
			t.Fatalf("diverge_returnurl cookie not set for %q", rawURL)
		}
		if returnCookie.Value != "/" {
			t.Errorf("HandleLogin return_url=%q set cookie %q, want \"/\"", rawURL, returnCookie.Value)
		}
	}
}

func makeUnsignedIDToken(t *testing.T, issuer, clientID, sub, email string, groups []string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := map[string]interface{}{
		"iss":                issuer,
		"aud":                clientID,
		"sub":                sub,
		"email":              email,
		"preferred_username": sub,
		"groups":             groups,
		"exp":                time.Now().Add(time.Hour).Unix(),
		"iat":                time.Now().Unix(),
	}
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	sig := base64.RawURLEncoding.EncodeToString([]byte("test-signature"))
	return header + "." + payload + "." + sig
}

func TestOIDCHandler_HandleCallback_AllowedGroupsAndRedirectSanitization(t *testing.T) {
	sm, err := auth.NewSessionManager(auth.SessionConfig{})
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	var currentGroups []string
	var issuerURL string
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawIDToken := makeUnsignedIDToken(t, issuerURL, "test-client", "alice", "alice@example.com", currentGroups)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
			"id_token":     rawIDToken,
		})
	}))
	defer tokenServer.Close()
	issuerURL = tokenServer.URL

	verifier := oidc.NewVerifier(issuerURL, nil, &oidc.Config{
		ClientID:                   "test-client",
		InsecureSkipSignatureCheck: true,
	})

	handler := &OIDCHandler{
		config: OIDCHandlerConfig{
			AllowedGroups: []string{"platform-admins", "developers"},
		},
		oauth2Config: oauth2.Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			Endpoint: oauth2.Endpoint{
				TokenURL: tokenServer.URL + "/token",
			},
		},
		verifier:       verifier,
		sessionManager: sm,
		sessionMaxAge:  time.Hour,
		usernameClaim:  "preferred_username",
		groupsClaim:    "groups",
		logger:         slog.Default(),
	}

	// 1. User NOT in AllowedGroups -> 403 Forbidden and NO diverge_token cookie
	currentGroups = []string{"contractors", "guests"}
	req := httptest.NewRequest("GET", "/auth/callback?code=validcode&state=goodstate", nil)
	req.AddCookie(&http.Cookie{Name: "diverge_oauthstate", Value: "goodstate"})
	rec := httptest.NewRecorder()
	handler.HandleCallback(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for disallowed group, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "User is not a member of any allowed group") {
		t.Errorf("expected disallowed group message in body, got: %s", rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "diverge_token" && c.Value != "" {
			t.Fatalf("expected diverge_token cookie NOT to be set for disallowed group, got %q", c.Value)
		}
	}

	// 2. User IN AllowedGroups with malicious return_url cookie -> 302 to "/" and diverge_token set
	currentGroups = []string{"developers"}
	req = httptest.NewRequest("GET", "/auth/callback?code=validcode&state=goodstate", nil)
	req.AddCookie(&http.Cookie{Name: "diverge_oauthstate", Value: "goodstate"})
	req.AddCookie(&http.Cookie{Name: "diverge_returnurl", Value: "//evil.example.com/pwn"})
	rec = httptest.NewRecorder()
	handler.HandleCallback(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 Found for allowed group, got %d: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("expected sanitized redirect Location \"/\", got %q", loc)
	}
	foundToken := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "diverge_token" && c.Value != "" {
			foundToken = true
		}
	}
	if !foundToken {
		t.Error("expected diverge_token cookie to be set for allowed group")
	}
}

func TestSanitizeReturnURL_PBT(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		prefix := hegel.Draw(ht, hegel.SampledFrom([]string{
			"",
			"/",
			"//",
			"/\\",
			"https://",
			"http://",
			"javascript:",
			"/dashboard/",
			"/environments?ns=",
		}))
		suffix := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(80))
		raw := prefix + suffix

		out := sanitizeReturnURL(raw)
		if !strings.HasPrefix(out, "/") {
			t.Fatalf("sanitizeReturnURL(%q) = %q, must start with \"/\"", raw, out)
		}
		if strings.HasPrefix(out, "//") {
			t.Fatalf("sanitizeReturnURL(%q) = %q, must not start with \"//\"", raw, out)
		}
		if strings.ContainsAny(out, "\\\r\n\t") {
			t.Fatalf("sanitizeReturnURL(%q) = %q, must not contain backslash or control whitespace", raw, out)
		}
		u, err := url.Parse(out)
		if err != nil {
			t.Fatalf("sanitizeReturnURL(%q) = %q failed url.Parse: %v", raw, out, err)
		}
		if u.Scheme != "" || u.Host != "" {
			t.Fatalf("sanitizeReturnURL(%q) = %q has non-empty Scheme=%q or Host=%q", raw, out, u.Scheme, u.Host)
		}
		if idempotent := sanitizeReturnURL(out); idempotent != out {
			t.Fatalf("sanitizeReturnURL is not idempotent: sanitizeReturnURL(%q) = %q != %q", out, idempotent, out)
		}
	})
}
