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

	corsAllowedOrigins := "*"
	corsMaxAge := 86400

	var allowedOrigins []string
	if corsAllowedOrigins == "*" {
		allowedOrigins = []string{"*"}
	} else {
		allowedOrigins = strings.Split(corsAllowedOrigins, ",")
	}

	corsHandler := cors.New(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"POST"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "Connect-Protocol-Version"},
		AllowCredentials: true,
		MaxAge:           corsMaxAge,
	})

	handler := corsHandler.Handler(dummyHandler)

	t.Run("OPTIONS preflight returns correct CORS headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/test", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "content-type")

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "POST")
		assert.Contains(t, strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers")), "content-type")
		assert.Equal(t, "86400", rec.Header().Get("Access-Control-Max-Age"))
	})

	t.Run("POST request includes CORS headers in response", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		req.Header.Set("Origin", "http://localhost:3000")

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "ok", rec.Body.String())
	})

	t.Run("Specific origins allowed", func(t *testing.T) {
		corsHandlerSpecific := cors.New(cors.Options{
			AllowedOrigins:   []string{"http://localhost:3000"},
			AllowedMethods:   []string{"POST"},
			AllowedHeaders:   []string{"Authorization", "Content-Type", "Connect-Protocol-Version"},
			AllowCredentials: true,
			MaxAge:           86400,
		})
		h := corsHandlerSpecific.Handler(dummyHandler)

		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		req.Header.Set("Origin", "http://localhost:3000")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "http://localhost:3000", rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("Disallowed origins rejected", func(t *testing.T) {
		corsHandlerSpecific := cors.New(cors.Options{
			AllowedOrigins:   []string{"http://localhost:3000"},
			AllowedMethods:   []string{"POST"},
			AllowedHeaders:   []string{"Authorization", "Content-Type", "Connect-Protocol-Version"},
			AllowCredentials: true,
			MaxAge:           86400,
		})
		h := corsHandlerSpecific.Handler(dummyHandler)

		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		req.Header.Set("Origin", "http://malicious.com")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	})
}
