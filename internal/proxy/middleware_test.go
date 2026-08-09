package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCORSMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := CORSMiddleware(inner)

	// With Origin header
	req := httptest.NewRequest("GET", "http://localhost/test", nil)
	req.Header.Set("Origin", "https://preview.example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, "https://preview.example.com", rr.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Origin", rr.Header().Get("Vary"))

	// Without Origin header — no CORS headers
	req2 := httptest.NewRequest("GET", "http://localhost/test", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	assert.Empty(t, rr2.Header().Get("Access-Control-Allow-Origin"))
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
