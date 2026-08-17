package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/cors"
	"github.com/stretchr/testify/assert"
)

func TestCORSHandler(t *testing.T) {
	// Dummy handler to act as the mux/auth middleware
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	setupCORS := func(corsAllowedOrigins string) http.Handler {
		corsMaxAge := 86400
		rawOrigins := strings.Split(corsAllowedOrigins, ",")
		origins := make([]string, 0, len(rawOrigins))
		for _, o := range rawOrigins {
			trimmed := strings.TrimSpace(o)
			if trimmed != "" {
				origins = append(origins, trimmed)
			}
		}

		opts := cors.Options{
			AllowedMethods:   []string{"GET", "POST"},
			AllowedHeaders:   []string{"Authorization", "Content-Type", "Connect-Protocol-Version", "Connect-Timeout-Ms", "X-Grpc-Web", "X-User-Agent", "Grpc-Timeout"},
			ExposedHeaders:   []string{"Grpc-Status", "Grpc-Message", "Grpc-Status-Details-Bin"},
			AllowCredentials: true,
			MaxAge:           corsMaxAge,
		}
		if len(origins) == 1 && origins[0] == "*" {
			opts.AllowOriginFunc = func(origin string) bool { return true }
		} else {
			opts.AllowedOrigins = origins
		}

		corsHandler := cors.New(opts)
		return corsHandler.Handler(dummyHandler)
	}

	handler := setupCORS("*")

	t.Run("OPTIONS preflight returns correct CORS headers including GET and new headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/test", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		req.Header.Set("Access-Control-Request-Method", "GET")
		req.Header.Set("Access-Control-Request-Headers", "content-type")

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNoContent, rec.Code)
		// With credentials and *, origin should be reflected instead of *
		assert.Equal(t, "http://localhost:3000", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
		assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "GET")
		assert.Contains(t, strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers")), "content-type")
		assert.Equal(t, "86400", rec.Header().Get("Access-Control-Max-Age"))
	})

	t.Run("POST request includes CORS headers and exposed headers in response", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		req.Header.Set("Origin", "http://localhost:3000")

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "http://localhost:3000", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
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
