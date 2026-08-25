package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

	req := httptest.NewRequest("GET", "/auth/callback", nil)
	rec := httptest.NewRecorder()
	handler.renderCallbackError(rec, req, "test error message")

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
