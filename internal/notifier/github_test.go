package notifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidateGitHubWebhookSignature(t *testing.T) {
	payload := []byte("hello world")
	secret := "supersecret"
	// Precomputed HMAC for "hello world" with key "supersecret"
	// signature: sha256=2b12bd89cc9485dfc1e549da75841ba0c091bc7df83c7dc5bb1e4a1a3e8cc160
	signature := "sha256=0cbf777626a72191cfc93476f80676a2cd944153eaf86310cf4cfb5910e67528"

	assert.True(t, ValidateGitHubWebhookSignature(payload, signature, secret))
	assert.False(t, ValidateGitHubWebhookSignature(payload, "sha256=invalid", secret))
	assert.False(t, ValidateGitHubWebhookSignature(payload, signature, "wrongsecret"))
	assert.False(t, ValidateGitHubWebhookSignature([]byte("wrong payload"), signature, secret))
}

func TestGitHubPreviewGroupNotifier_PostGroupCreated(t *testing.T) {
	var requestedPath string
	var requestedMethod string
	var requestedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		requestedMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&requestedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 12345}`))
	}))
	defer server.Close()

	n := NewGitHubPreviewGroupNotifier(server.URL, "token")
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pg"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}

	err := n.PostGroupCreated(context.Background(), pg)
	assert.NoError(t, err)

	assert.Equal(t, http.MethodPost, requestedMethod)
	assert.Equal(t, "/repos/owner/repo/issues/42/comments", requestedPath)
	assert.Equal(t, int64(12345), pg.Status.CommentID)
	assert.Contains(t, requestedBody["body"], "test-pg")
}

func TestGitHubPreviewGroupNotifier_UpdateGroupStatus(t *testing.T) {
	var requestedPath string
	var requestedMethod string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		requestedMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 12345}`))
	}))
	defer server.Close()

	n := NewGitHubPreviewGroupNotifier(server.URL, "token")
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pg"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "owner/repo",
				MR:       42,
			},
		},
		Status: v1alpha1.PreviewGroupStatus{
			CommentID: 12345,
		},
	}

	err := n.UpdateGroupStatus(context.Background(), pg)
	assert.NoError(t, err)

	assert.Equal(t, http.MethodPatch, requestedMethod)
	assert.Equal(t, "/repos/owner/repo/issues/comments/12345", requestedPath)
}

func TestGitHubPreviewGroupNotifier_RecreateOn404(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			// First call (PATCH) returns 404
			assert.Equal(t, http.MethodPatch, r.Method)
			w.WriteHeader(http.StatusNotFound)
		} else {
			// Second call (POST) succeeds
			assert.Equal(t, http.MethodPost, r.Method)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 9999}`))
		}
	}))
	defer server.Close()

	n := NewGitHubPreviewGroupNotifier(server.URL, "token")
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pg"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "owner/repo",
				MR:       42,
			},
		},
		Status: v1alpha1.PreviewGroupStatus{
			CommentID: 12345,
		},
	}

	err := n.UpdateGroupStatus(context.Background(), pg)
	assert.NoError(t, err)
	assert.Equal(t, 2, callCount)
	assert.Equal(t, int64(9999), pg.Status.CommentID)
}

func TestGitHubPreviewGroupNotifier_RateLimitBackoff(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			// First call returns 429 Too Many Requests
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
		} else {
			// Second call succeeds
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 1111}`))
		}
	}))
	defer server.Close()

	n := NewGitHubPreviewGroupNotifier(server.URL, "token")
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pg"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}

	start := time.Now()
	err := n.PostGroupCreated(context.Background(), pg)
	duration := time.Since(start)

	assert.NoError(t, err)
	assert.Equal(t, 2, callCount)
	assert.Equal(t, int64(1111), pg.Status.CommentID)
	assert.True(t, duration >= time.Second, "expected backoff delay of at least 1 second, got %v", duration)
}

func TestResolveDispatchRun_HappyPath(t *testing.T) {
	dispatchedAt := time.Now()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Contains(t, r.URL.Path, "/repos/owner/repo/actions/runs")
		assert.Contains(t, r.URL.RawQuery, "event=workflow_dispatch")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"workflow_runs": [
				{
					"id": 8888,
					"created_at": "` + dispatchedAt.Add(5*time.Second).Format(time.RFC3339) + `"
				}
			]
		}`))
	}))
	defer server.Close()

	n := NewGitHubNotifier(server.URL, "token")
	// Make ticker fast for test
	runID, err := n.resolveDispatchRun(context.Background(), "owner", "repo", "ci.yml", "main", dispatchedAt)
	assert.NoError(t, err)
	assert.Equal(t, int64(8888), runID)
}

func TestResolveDispatchRun_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"workflow_runs": []}`))
	}))
	defer server.Close()

	n := NewGitHubNotifier(server.URL, "token")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := n.resolveDispatchRun(ctx, "owner", "repo", "ci.yml", "main", time.Now())
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestResolveDispatchRun_MultipleRuns(t *testing.T) {
	dispatchedAt := time.Now()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Return an old run and a new run
		_, _ = w.Write([]byte(`{
			"workflow_runs": [
				{
					"id": 1111,
					"created_at": "` + dispatchedAt.Add(-20*time.Second).Format(time.RFC3339) + `"
				},
				{
					"id": 9999,
					"created_at": "` + dispatchedAt.Add(2*time.Second).Format(time.RFC3339) + `"
				}
			]
		}`))
	}))
	defer server.Close()

	n := NewGitHubNotifier(server.URL, "token")
	runID, err := n.resolveDispatchRun(context.Background(), "owner", "repo", "ci.yml", "main", dispatchedAt)
	assert.NoError(t, err)
	assert.Equal(t, int64(9999), runID)
}

func TestResolveDispatchRun_RateLimit(t *testing.T) {
	dispatchedAt := time.Now()
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"workflow_runs": [
				{
					"id": 8888,
					"created_at": "` + dispatchedAt.Add(5*time.Second).Format(time.RFC3339) + `"
				}
			]
		}`))
	}))
	defer server.Close()

	n := NewGitHubNotifier(server.URL, "token")
	start := time.Now()
	runID, err := n.resolveDispatchRun(context.Background(), "owner", "repo", "ci.yml", "main", dispatchedAt)
	assert.NoError(t, err)
	assert.Equal(t, int64(8888), runID)
	assert.True(t, time.Since(start) >= time.Second)
}

func TestResolveDispatchRun_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	n := NewGitHubNotifier(server.URL, "token")
	_, err := n.resolveDispatchRun(context.Background(), "owner", "repo", "ci.yml", "main", time.Now())
	assert.ErrorContains(t, err, "GitHub API error: 500")
}

func TestResolveDispatchRun_ContextCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"workflow_runs": []}`))
	}))
	defer server.Close()

	n := NewGitHubNotifier(server.URL, "token")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := n.resolveDispatchRun(ctx, "owner", "repo", "ci.yml", "main", time.Now())
	assert.ErrorIs(t, err, context.Canceled)
}
