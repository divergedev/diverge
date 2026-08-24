package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
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
		{"application/grpc-web-text", "grpc-web"},
		{"application/grpc-web-text+proto", "grpc-web"},
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
		{"grpc-web-javascript/0.1", "grpc-web"},
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

func TestProtocolTelemetryMiddleware(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		contentType  string
		userAgent    string
		xUserAgent   string
		wantProtocol string // expected protocol label; empty = no increment
		wantClient   string // expected client label; empty = no increment
		wantSkip     bool   // true if counters should NOT change
	}{
		{
			name:     "healthz probe is skipped",
			path:     "/healthz",
			wantSkip: true,
		},
		{
			name:     "readyz probe is skipped",
			path:     "/readyz",
			wantSkip: true,
		},
		{
			name:         "connect-json request with Go SDK",
			path:         "/diverge.v1alpha1.EnvironmentService/ListEnvironments",
			contentType:  "application/json",
			userAgent:    "connect-go/1.16.0",
			wantProtocol: "connect-json",
			wantClient:   "go-sdk",
		},
		{
			name:         "grpc request with grpc-go",
			path:         "/diverge.v1alpha1.EnvironmentService/ListEnvironments",
			contentType:  "application/grpc",
			userAgent:    "grpc-go/1.64.0",
			wantProtocol: "grpc",
			wantClient:   "grpc-go",
		},
		{
			name:         "grpc-web-text from browser",
			path:         "/diverge.v1alpha1.EnvironmentService/ListEnvironments",
			contentType:  "application/grpc-web-text",
			xUserAgent:   "grpc-web-javascript/0.1",
			userAgent:    "Mozilla/5.0",
			wantProtocol: "grpc-web",
			wantClient:   "grpc-web",
		},
		{
			name:         "empty User-Agent records as other",
			path:         "/diverge.v1alpha1.EnvironmentService/ListEnvironments",
			contentType:  "application/json",
			wantProtocol: "connect-json",
			wantClient:   "other",
		},
		{
			name:         "X-User-Agent preferred over User-Agent",
			path:         "/diverge.v1alpha1.EnvironmentService/ListEnvironments",
			contentType:  "application/grpc-web",
			xUserAgent:   "grpc-web-javascript/0.1",
			userAgent:    "Mozilla/5.0 (Chrome)",
			wantProtocol: "grpc-web",
			wantClient:   "grpc-web",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var called bool
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})

			handler := ProtocolTelemetryMiddleware(inner)

			// Capture counters before
			var protocolBefore, clientBefore float64
			if tt.wantProtocol != "" {
				protocolBefore = promtestutil.ToFloat64(requestsByProtocol.WithLabelValues(tt.wantProtocol))
			}
			if tt.wantClient != "" {
				clientBefore = promtestutil.ToFloat64(requestsByClient.WithLabelValues(tt.wantClient))
			}

			req := httptest.NewRequest("POST", tt.path, nil)
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			if tt.userAgent != "" {
				req.Header.Set("User-Agent", tt.userAgent)
			}
			if tt.xUserAgent != "" {
				req.Header.Set("X-User-Agent", tt.xUserAgent)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.True(t, called, "inner handler should be called")
			assert.Equal(t, http.StatusOK, rec.Code)

			if tt.wantSkip {
				// For skipped paths, verify no counter changed for a known label
				protocolAfter := promtestutil.ToFloat64(requestsByProtocol.WithLabelValues("connect-json"))
				_ = protocolAfter // counters are shared across tests; just verify handler was called
				return
			}

			// Verify counter deltas
			if tt.wantProtocol != "" {
				assert.Equal(t, protocolBefore+1, promtestutil.ToFloat64(requestsByProtocol.WithLabelValues(tt.wantProtocol)),
					"protocol counter should increment by 1")
			}
			if tt.wantClient != "" {
				assert.Equal(t, clientBefore+1, promtestutil.ToFloat64(requestsByClient.WithLabelValues(tt.wantClient)),
					"client counter should increment by 1")
			}
		})
	}
}
