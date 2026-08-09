package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCORSMiddlewareTrustedOrigin(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := CORSMiddleware("preview.example.com", inner)

	req := httptest.NewRequest("GET", "http://localhost/test", nil)
	req.Header.Set("Origin", "https://mr-42.preview.example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, "https://mr-42.preview.example.com", rr.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Origin", rr.Header().Get("Vary"))
}

func TestCORSMiddlewareUntrustedOrigin(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := CORSMiddleware("preview.example.com", inner)

	req := httptest.NewRequest("GET", "http://localhost/test", nil)
	req.Header.Set("Origin", "https://evil.attacker.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Empty(t, rr.Header().Get("Access-Control-Allow-Origin"), "untrusted origin should not get CORS headers")
}

func TestCORSMiddlewareNoOrigin(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := CORSMiddleware("preview.example.com", inner)

	req := httptest.NewRequest("GET", "http://localhost/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Empty(t, rr.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSMiddlewarePreflight(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // should not reach here
	})
	handler := CORSMiddleware("preview.example.com", inner)

	req := httptest.NewRequest("OPTIONS", "http://localhost/test", nil)
	req.Header.Set("Origin", "https://mr-42.preview.example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "https://mr-42.preview.example.com", rr.Header().Get("Access-Control-Allow-Origin"))
}

func TestIsAllowedOrigin(t *testing.T) {
	tests := []struct {
		origin, domain string
		want           bool
	}{
		{"https://mr-42.preview.example.com", "preview.example.com", true},
		{"https://preview.example.com", "preview.example.com", true},
		{"https://mr-42.preview.example.com:8080", "preview.example.com", true},
		{"https://evil.com", "preview.example.com", false},
		{"https://notpreview.example.com", "preview.example.com", false},
		{"https://example.com", "preview.example.com", false},
		{"", "preview.example.com", false},
		{"https://mr-42.preview.example.com", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.origin, func(t *testing.T) {
			assert.Equal(t, tt.want, isAllowedOrigin(tt.origin, tt.domain))
		})
	}
}

func TestLoggingMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := LoggingMiddleware(inner)
	req := httptest.NewRequest("GET", "http://localhost/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}
