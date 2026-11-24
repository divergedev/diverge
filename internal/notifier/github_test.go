package notifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		if r.Method == http.MethodGet && r.URL.Path == "/user" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": 99, "login": "diverge-bot"}`))
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}
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
		if r.Method == http.MethodGet && r.URL.Path == "/user" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": 99, "login": "diverge-bot"}`))
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}
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

func TestGitHubRegistryProvidersHonorBaseURLEnv(t *testing.T) {
	t.Setenv("DIVERGE_NOTIFIER_TOKEN", "ghp_test")
	t.Setenv("DIVERGE_NOTIFIER_BASE_URL", "https://ghe.example.com/api/v3/")
	t.Setenv("GITHUB_API_URL", "https://fallback.example.com/api/v3")
	t.Setenv("DIVERGE_BASE_URL", "https://staging.app.example.com")

	n, err := Providers.Create("github", registry.Deps{})
	require.NoError(t, err)
	assert.Equal(t, "https://ghe.example.com/api/v3", n.(*GitHubNotifier).BaseURL)

	sr, err := StatusProviders.Create("github", registry.Deps{})
	require.NoError(t, err)
	assert.Equal(t, "https://ghe.example.com/api/v3", sr.(*GitHubStatusReporter).BaseURL)

	pgn, err := GroupProviders.Create("github", registry.Deps{})
	require.NoError(t, err)
	assert.Equal(t, "https://ghe.example.com/api/v3", pgn.(*GitHubPreviewGroupNotifier).BaseURL)

	// Verify DIVERGE_BASE_URL (application replay baseline) never hijacks GitHub API requests.
	t.Setenv("DIVERGE_NOTIFIER_BASE_URL", "")
	t.Setenv("GITHUB_API_URL", "")
	nDefault, err := Providers.Create("github", registry.Deps{})
	require.NoError(t, err)
	assert.Equal(t, "https://api.github.com", nDefault.(*GitHubNotifier).BaseURL)
}

func TestGitHubPreviewGroupNotifier_RecoversExistingCommentByMarker(t *testing.T) {
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-recover"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}
	marker := previewGroupCommentMarker(pg)
	var methods []string
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/user" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": 99, "login": "diverge-bot"}`))
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": 1001, "body": "other comment <!-- diverge:previewgroup=other source=github:owner/repo#42 -->", "user": map[string]interface{}{"id": 99, "login": "diverge-bot"}},
				{"id": 7777, "body": "previous status\n\n" + marker, "user": map[string]interface{}{"id": 99, "login": "diverge-bot"}},
				{"id": 8888, "body": "human quoted\n\n" + marker, "user": map[string]interface{}{"id": 500, "login": "alice"}},
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 7777}`))
	}))
	defer server.Close()

	n := NewGitHubPreviewGroupNotifier(server.URL, "token")
	require.NoError(t, n.PostGroupReady(context.Background(), pg))
	assert.Equal(t, []string{http.MethodGet, http.MethodGet, http.MethodPatch}, methods)
	assert.Equal(t, []string{"/user", "/repos/owner/repo/issues/42/comments", "/repos/owner/repo/issues/comments/7777"}, paths)
	assert.Equal(t, int64(7777), pg.Status.CommentID)
}

func TestGitHubPreviewGroupNotifier_IgnoresHumanQuotedMarkerAndReusesLearnedAuthor(t *testing.T) {
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-learned-author"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}
	marker := previewGroupCommentMarker(pg)
	allowUserCall := true
	commentsList := []map[string]interface{}{
		{"id": 8888, "body": "human quote\n\n" + marker, "user": map[string]interface{}{"id": 500, "login": "alice"}},
	}
	var methods []string
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/user" {
			if !allowUserCall {
				t.Fatalf("unexpected /user request after author identity was learned")
			}
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(commentsList)
			return
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 7777, "user": {"id": 99, "login": "diverge-bot"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 7777}`))
	}))
	defer server.Close()

	n := NewGitHubPreviewGroupNotifier(server.URL, "token")
	require.NoError(t, n.PostGroupCreated(context.Background(), pg))
	assert.Equal(t, []string{http.MethodGet, http.MethodPost}, methods)
	assert.Equal(t, []string{"/user", "/repos/owner/repo/issues/42/comments"}, paths)
	assert.Equal(t, int64(7777), pg.Status.CommentID)

	// Simulate status conflict dropping CommentID and adding the newly created bot comment before retry
	allowUserCall = false
	pg.Status.CommentID = 0
	commentsList = append(commentsList, map[string]interface{}{
		"id": 7777, "body": "created\n\n" + marker, "user": map[string]interface{}{"id": 99, "login": "diverge-bot"},
	})
	methods = nil
	paths = nil

	require.NoError(t, n.PostGroupReady(context.Background(), pg))
	assert.Equal(t, []string{http.MethodGet, http.MethodPatch}, methods)
	assert.Equal(t, []string{"/repos/owner/repo/issues/42/comments", "/repos/owner/repo/issues/comments/7777"}, paths)
	assert.Equal(t, int64(7777), pg.Status.CommentID)
}

func TestGitHubPreviewGroupNotifier_RecoveredIDStaysProvisionalOnPatchFailure(t *testing.T) {
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-provisional"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}
	marker := previewGroupCommentMarker(pg)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/user" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": 99, "login": "diverge-bot"}`))
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": 7777, "body": marker, "user": map[string]interface{}{"id": 99, "login": "diverge-bot"}},
			})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	n := NewGitHubPreviewGroupNotifier(server.URL, "token")
	err := n.PostGroupReady(context.Background(), pg)
	require.Error(t, err)
	assert.Zero(t, pg.Status.CommentID, "recovered comment ID must stay provisional until PATCH succeeds")
}

func TestGitHubPreviewGroupNotifier_TemporaryUserLookupFailureRetainsExistingComment(t *testing.T) {
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-temp-user-err"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}
	marker := previewGroupCommentMarker(pg)
	userStep := 0
	var methods []string
	var postCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/user" {
			userStep++
			switch userStep {
			case 1:
				w.WriteHeader(http.StatusInternalServerError)
			case 2:
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id": 99, "login":`))
			default:
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id": 99, "login": "diverge-bot"}`))
			}
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": 7777, "body": "existing\n\n" + marker, "user": map[string]interface{}{"id": 99, "login": "diverge-bot"}},
			})
			return
		}
		if r.Method == http.MethodPost {
			postCount++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 8888}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 7777}`))
	}))
	defer server.Close()

	n := NewGitHubPreviewGroupNotifier(server.URL, "token")
	err := n.PostGroupReady(context.Background(), pg)
	require.Error(t, err, "temporary /user 500 must fail recovery rather than posting a duplicate")
	assert.Zero(t, postCount)
	assert.Zero(t, pg.Status.CommentID)

	err = n.PostGroupReady(context.Background(), pg)
	require.Error(t, err, "truncated /user JSON must return decode error rather than marking checked or posting a duplicate")
	assert.Zero(t, postCount)
	assert.Zero(t, pg.Status.CommentID)

	require.NoError(t, n.PostGroupReady(context.Background(), pg))
	assert.Zero(t, postCount, "recovery after /user recovers must PATCH existing comment instead of POSTing duplicate")
	assert.Equal(t, []string{http.MethodGet, http.MethodGet, http.MethodGet, http.MethodGet, http.MethodPatch}, methods)
	assert.Equal(t, int64(7777), pg.Status.CommentID)
}

func TestGitHubPreviewGroupNotifier_404FallbackAndExhaustion(t *testing.T) {
	newPG := func(commentID int64) *v1alpha1.PreviewGroup {
		return &v1alpha1.PreviewGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "pg-gh-404-retry"},
			Spec: v1alpha1.PreviewGroupSpec{
				Source: v1alpha1.EnvironmentSource{
					Provider: "github",
					Project:  "owner/repo",
					MR:       42,
				},
			},
			Status: v1alpha1.PreviewGroupStatus{
				CommentID: commentID,
			},
		}
	}

	t.Run("404 on PATCH falls back to POST and succeeds", func(t *testing.T) {
		pg := newPG(7777)
		var methods []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			methods = append(methods, r.Method)
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodPatch {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id": 8888, "user": {"id": 99, "login": "diverge-bot"}}`))
				return
			}
			t.Fatalf("unexpected method %s", r.Method)
		}))
		defer server.Close()

		n := NewGitHubPreviewGroupNotifier(server.URL, "token")
		require.NoError(t, n.PostGroupReady(context.Background(), pg))
		assert.Equal(t, []string{http.MethodPatch, http.MethodPost}, methods)
		assert.Equal(t, int64(8888), pg.Status.CommentID)
	})

	t.Run("429 followed by 404 on final attempt returns error", func(t *testing.T) {
		pg := newPG(7777)
		var patchCount int
		var postCount int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodPatch {
				patchCount++
				if patchCount < 4 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.Method == http.MethodPost {
				postCount++
			}
			t.Fatalf("unexpected method %s", r.Method)
		}))
		defer server.Close()

		n := NewGitHubPreviewGroupNotifier(server.URL, "token")
		require.Error(t, n.PostGroupReady(context.Background(), pg))
		assert.Equal(t, 4, patchCount)
		assert.Zero(t, postCount)
		assert.Zero(t, pg.Status.CommentID)
	})
}
