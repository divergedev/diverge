package webhook

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"hegel.dev/go/hegel"
)

func TestGitLabWebhookAlwaysValidatesToken(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		token := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(100))
		secretToken := "correct-secret-token"

		config := WebhookConfig{SecretToken: secretToken}
		handler := &GitLabWebhookHandler{Config: config}

		req, _ := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{}`))
		req.Header.Set("X-Gitlab-Token", token)

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if token != secretToken {
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("Expected 401 for token %q, got %d", token, rr.Code)
			}
		}
	})
}
