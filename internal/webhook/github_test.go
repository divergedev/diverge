package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func generateSignature(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestGitHubWebhookHMACValidation(t *testing.T) {
	config := WebhookConfig{SecretToken: "secret"}
	handler := &GitHubWebhookHandler{Config: config}
	body := `{"action": "opened"}`
	validSig := generateSignature("secret", body)

	tests := []struct {
		name       string
		signature  string
		statusCode int
	}{
		{
			name:       "valid signature",
			signature:  validSig,
			statusCode: http.StatusOK,
		},
		{
			name:       "invalid signature",
			signature:  "sha256=invalid",
			statusCode: http.StatusUnauthorized,
		},
		{
			name:       "missing signature",
			signature:  "",
			statusCode: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
			req.Header.Set("X-GitHub-Event", "pull_request")
			if tt.signature != "" {
				req.Header.Set("X-Hub-Signature-256", tt.signature)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.statusCode {
				t.Errorf("Expected status %d, got %d", tt.statusCode, rr.Code)
			}
		})
	}
}

func TestGitHubWebhookMalformedPayload(t *testing.T) {
	config := WebhookConfig{SecretToken: "secret"}
	handler := &GitHubWebhookHandler{Config: config}

	body := `{bad json`
	sig := generateSignature("secret", body)

	req, _ := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Event", "pull_request")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for malformed json, got %d", rr.Code)
	}
}
