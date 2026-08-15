package webhook

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
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
			require.Equalf(ht, http.StatusUnauthorized, rr.Code, "Expected 401 for token %q, got %d", token, rr.Code)
		}
	})
}

func TestNormalizeGitLabAction_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		action := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(20))
		normalized := normalizeGitLabAction(action)
		require.Contains(ht, []string{"open", "reopen", "update", "merge", "close", "approved", "unapproved", "other"}, normalized, "unexpected normalized action: %s", normalized)
	})
}

func TestNormalizeGitHubAction_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		action := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(20))
		normalized := normalizeGitHubAction(action)
		require.Contains(ht, []string{"opened", "synchronize", "closed", "reopened", "edited", "ready_for_review", "other"}, normalized, "unexpected normalized action: %s", normalized)
	})
}

func TestSafeSHA_Property_NeverPanics(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		s := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(100))
		maxLen := hegel.Draw(ht, hegel.Integers(0, 1000))

		require.NotPanics(ht, func() {
			_ = safeSHA(s, maxLen)
		})
	})
}

func TestSafeSHA_Property_LengthBound(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		s := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(100))
		maxLen := hegel.Draw(ht, hegel.Integers(0, 1000))

		res := safeSHA(s, maxLen)
		require.LessOrEqual(ht, len(res), maxLen)
	})
}

func TestSafeSHA_Property_Prefix(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		s := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(100))
		maxLen := hegel.Draw(ht, hegel.Integers(0, 1000))

		if len(s) > maxLen {
			res := safeSHA(s, maxLen)
			require.Equal(ht, s[:maxLen], res)
		}
	})
}

func TestSafeSHA_Property_Identity(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		s := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(100))
		maxLen := hegel.Draw(ht, hegel.Integers(0, 1000))

		if len(s) <= maxLen {
			res := safeSHA(s, maxLen)
			require.Equal(ht, s, res)
		}
	})
}
