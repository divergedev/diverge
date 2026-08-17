package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divergedev/diverge/internal/server/auth"
)

func TestAuditLogger(t *testing.T) {
	cases := []struct {
		name       string
		action     func(*AuditLogger)
		wantLevel  string
		wantEvent  string
		wantFields map[string]interface{}
	}{
		{
			name: "auth.success -> INFO",
			action: func(a *AuditLogger) {
				req, _ := http.NewRequest("GET", "/api", nil)
				req.RemoteAddr = "10.0.0.1"
				a.LogAuth(context.Background(), "auth.success", &auth.UserInfo{Username: "test"}, req)
			},
			wantLevel: "INFO",
			wantEvent: "auth.success",
			wantFields: map[string]interface{}{
				"user":      "test",
				"source_ip": "10.0.0.1",
			},
		},
		{
			name: "auth.failure -> WARN",
			action: func(a *AuditLogger) {
				req, _ := http.NewRequest("GET", "/api", nil)
				a.LogAuth(context.Background(), "auth.failure", nil, req)
			},
			wantLevel:  "WARN",
			wantEvent:  "auth.failure",
			wantFields: map[string]interface{}{},
		},
		{
			name: "authz.denied -> WARN",
			action: func(a *AuditLogger) {
				a.LogAuthz(context.Background(), "authz.denied", &auth.UserInfo{Username: "test"}, "get", "pods", "default")
			},
			wantLevel: "WARN",
			wantEvent: "authz.denied",
			wantFields: map[string]interface{}{
				"user": "test",
				"verb": "get",
			},
		},
		{
			name: "authz.error -> WARN",
			action: func(a *AuditLogger) {
				a.LogAuthz(context.Background(), "authz.error", &auth.UserInfo{Username: "test"}, "get", "pods", "default")
			},
			wantLevel: "WARN",
			wantEvent: "authz.error",
			wantFields: map[string]interface{}{
				"user": "test",
			},
		},
		{
			name: "resource.created -> INFO",
			action: func(a *AuditLogger) {
				ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "admin"})
				a.LogMutation(ctx, "resource.created", "pod", "my-pod", "default")
			},
			wantLevel: "INFO",
			wantEvent: "resource.created",
			wantFields: map[string]interface{}{
				"user":          "admin",
				"resource_type": "pod",
				"name":          "my-pod",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			handler := slog.NewJSONHandler(&buf, nil)
			logger := slog.New(handler)
			auditLogger := NewAuditLogger(logger)

			tc.action(auditLogger)

			var logEntry map[string]interface{}
			err := json.Unmarshal(buf.Bytes(), &logEntry)
			require.NoError(t, err)

			assert.Equal(t, tc.wantLevel, logEntry["level"])
			assert.Equal(t, "audit", logEntry["msg"])
			assert.Equal(t, "audit", logEntry["component"])
			assert.Equal(t, tc.wantEvent, logEntry["event"])

			for k, v := range tc.wantFields {
				assert.Equal(t, v, logEntry[k])
			}
		})
	}
}

func TestSourceIP(t *testing.T) {
	req1, _ := http.NewRequest("GET", "/", nil)
	req1.RemoteAddr = "192.168.1.1:1234"
	assert.Equal(t, "192.168.1.1:1234", sourceIP(req1))

	req2, _ := http.NewRequest("GET", "/", nil)
	req2.Header.Set("X-Forwarded-For", "10.0.0.1")
	req2.RemoteAddr = "192.168.1.1:1234"
	assert.Equal(t, "10.0.0.1", sourceIP(req2))

	assert.Equal(t, "", sourceIP(nil))
}
