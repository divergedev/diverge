package webhook

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitLabWebhookTokenValidation(t *testing.T) {
	config := WebhookConfig{SecretToken: "secret-token"}
	handler := &GitLabWebhookHandler{Config: config}

	tests := []struct {
		name       string
		token      string
		body       string
		statusCode int
	}{
		{
			name:       "valid token",
			token:      "secret-token",
			body:       `{"object_kind": "merge_request", "object_attributes": {"iid": 1, "action": "open"}}`,
			statusCode: http.StatusOK,
		},
		{
			name:       "invalid token",
			token:      "wrong-token",
			body:       `{}`,
			statusCode: http.StatusUnauthorized,
		},
		{
			name:       "missing token",
			token:      "",
			body:       `{}`,
			statusCode: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(tt.body))
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}
			req.Header.Set("X-Gitlab-Token", tt.token)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.statusCode {
				t.Errorf("Expected status %d, got %d", tt.statusCode, rr.Code)
			}
		})
	}
}

func TestGitLabWebhookMalformedJSON(t *testing.T) {
	config := WebhookConfig{SecretToken: "secret"}
	handler := &GitLabWebhookHandler{Config: config}

	req, _ := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{bad json`))
	req.Header.Set("X-Gitlab-Token", "secret")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for malformed json, got %d", rr.Code)
	}
}

func TestGitLabWebhookUnsupportedEvent(t *testing.T) {
	config := WebhookConfig{SecretToken: "secret"}
	handler := &GitLabWebhookHandler{Config: config}

	req, _ := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"object_kind": "push"}`))
	req.Header.Set("X-Gitlab-Token", "secret")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200 for unsupported event, got %d", rr.Code)
	}
}
