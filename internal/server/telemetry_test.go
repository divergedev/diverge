package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectProtocol(t *testing.T) {
	tests := []struct {
		contentType string
		expected    string
	}{
		{"application/grpc", "grpc"},
		{"application/grpc+proto", "grpc"},
		{"application/grpc-web", "grpc-web"},
		{"application/grpc-web+proto", "grpc-web"},
		{"application/grpc-web+json", "grpc-web"},
		{"application/json", "connect-json"},
		{"application/json; charset=utf-8", "connect-json"},
		{"application/proto", "connect-proto"},
		{"application/connect+json", "connect-stream-json"},
		{"application/connect+proto", "connect-stream-proto"},
		{"text/html", "unknown"},
		{"", "unknown"},
		{"  Application/JSON  ", "connect-json"},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			assert.Equal(t, tt.expected, detectProtocol(tt.contentType))
		})
	}
}

func TestDetectClient(t *testing.T) {
	tests := []struct {
		userAgent string
		expected  string
	}{
		{"connect-go/1.16.0", "go-sdk"},
		{"connect-es/1.4.0", "ts-sdk"},
		{"grpc-go/1.64.0", "grpc-go"},
		{"grpc-web/1.5.0", "grpc-web"},
		{"curl/8.5.0", "curl"},
		{"buf/1.30.0", "buf"},
		{"Mozilla/5.0", "other"},
		{"", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.userAgent, func(t *testing.T) {
			assert.Equal(t, tt.expected, detectClient(tt.userAgent))
		})
	}
}

func TestProtocolTelemetryMiddleware_SkipsHealthChecks(t *testing.T) {
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := ProtocolTelemetryMiddleware(inner)

	// Health check should pass through without recording
	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestProtocolTelemetryMiddleware_RecordsProtocol(t *testing.T) {
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := ProtocolTelemetryMiddleware(inner)

	req := httptest.NewRequest("POST", "/diverge.v1alpha1.EnvironmentService/ListEnvironments", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "connect-go/1.16.0")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}
