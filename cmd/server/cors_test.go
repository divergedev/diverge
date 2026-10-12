package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/cors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/internal/server"
	"github.com/divergedev/diverge/internal/server/auth"
)

type rejectAllAuthProvider struct{}

func (rejectAllAuthProvider) Authenticate(_ context.Context, _ string) (*auth.UserInfo, error) {
	return nil, errors.New("unauthorized")
}

func TestCORSHandler(t *testing.T) {
	// Dummy handler to act as the mux/auth middleware
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	setupCORS := func(corsAllowedOrigins string) http.Handler {
		opts := newCORSOptions(corsAllowedOrigins, 86400)
		assert.False(t, opts.AllowCredentials)
		corsHandler := cors.New(opts)
		return corsHandler.Handler(dummyHandler)
	}

	handler := setupCORS("*")

	t.Run("OPTIONS preflight returns correct CORS headers with AllowCredentials false", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/test", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		req.Header.Set("Access-Control-Request-Method", "GET")
		req.Header.Set("Access-Control-Request-Headers", "content-type")

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.Equal(t, "http://localhost:3000", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
		assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "GET")
		assert.Contains(t, strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers")), "content-type")
		assert.Equal(t, "86400", rec.Header().Get("Access-Control-Max-Age"))
	})

	t.Run("POST request includes CORS headers and exposed headers without credentials", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		req.Header.Set("Origin", "http://localhost:3000")

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "http://localhost:3000", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
		assert.Contains(t, rec.Header().Get("Access-Control-Expose-Headers"), "Grpc-Status")
		assert.Equal(t, "ok", rec.Body.String())
	})

	t.Run("Specific origins allowed and trimmed", func(t *testing.T) {
		h := setupCORS(" http://localhost:3000 , http://example.com  ")

		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		req.Header.Set("Origin", "http://example.com")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "http://example.com", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
	})

	t.Run("Disallowed origins rejected", func(t *testing.T) {
		h := setupCORS("http://localhost:3000")

		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		req.Header.Set("Origin", "http://malicious.com")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	})
}

func TestDashboardAndLogoutAuthExemptions(t *testing.T) {
	mux, _ := server.NewServeMux(server.ServeMuxConfig{
		Client:           fake.NewClientBuilder().Build(),
		DashboardEnabled: true,
	})

	t.Run("OIDC disabled: GET / and GET /login serve dashboard without auth", func(t *testing.T) {
		exemptPrefixes := buildExemptPrefixes(true, false)
		mw := auth.NewMiddleware(auth.MiddlewareConfig{
			Provider:       rejectAllAuthProvider{},
			Cache:          auth.NewTokenCache(16, time.Minute),
			Logger:         slog.Default(),
			ExemptPaths:    []string{"/healthz", "/readyz"},
			ExemptPrefixes: exemptPrefixes,
			LoginURL:       "",
		})
		h := wrapDashboardAuthHandler(mw(mux), mux, true, "")

		for _, path := range []string{"/", "/login", "/favicon.ico"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Accept", "text/html")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, "expected 200 for %s when OIDC is disabled", path)
		}
	})

	t.Run("OIDC enabled: GET /login is exempt (preventing logout redirect loop) while GET / redirects to /auth/login", func(t *testing.T) {
		exemptPrefixes := buildExemptPrefixes(true, true)
		mw := auth.NewMiddleware(auth.MiddlewareConfig{
			Provider:       rejectAllAuthProvider{},
			Cache:          auth.NewTokenCache(16, time.Minute),
			Logger:         slog.Default(),
			ExemptPaths:    []string{"/healthz", "/readyz"},
			ExemptPrefixes: exemptPrefixes,
			LoginURL:       "/auth/login",
		})
		h := wrapDashboardAuthHandler(mw(mux), mux, true, "/auth/login")

		// /login loads SPA without redirecting back to /auth/login
		loginReq := httptest.NewRequest(http.MethodGet, "/login", nil)
		loginReq.Header.Set("Accept", "text/html")
		loginRec := httptest.NewRecorder()
		h.ServeHTTP(loginRec, loginReq)
		require.Equal(t, http.StatusOK, loginRec.Code)

		// / redirects browser to /auth/login
		rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
		rootReq.Header.Set("Accept", "text/html")
		rootRec := httptest.NewRecorder()
		h.ServeHTTP(rootRec, rootReq)
		require.Equal(t, http.StatusSeeOther, rootRec.Code)
		assert.Equal(t, "/auth/login", rootRec.Header().Get("Location"))
	})
}
