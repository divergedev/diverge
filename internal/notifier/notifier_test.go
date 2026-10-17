package notifier

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGitLabNotifierPostComment(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v4/projects/my/repo/merge_requests/42/notes", r.URL.Path)
		assert.Equal(t, "mytoken", r.Header.Get("PRIVATE-TOKEN"))

		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		require.NoError(t, json.Unmarshal(body, &payload))
		assert.Contains(t, payload["body"], "test message")

		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 12345}`))
	}))
	defer ts.Close()

	notifier := NewGitLabNotifier(ts.URL, "mytoken")
	env := &v1alpha1.Environment{
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "my/repo",
				MR:       42,
			},
		},
	}

	err := notifier.postOrUpdateComment(context.Background(), env, "test message")
	assert.NoError(t, err)
	assert.Equal(t, 12345, env.Status.CommentID)
}

func TestGitLabNotifierUpdateComment(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/api/v4/projects/my/repo/merge_requests/42/notes/12345", r.URL.Path)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 12345}`))
	}))
	defer ts.Close()

	notifier := NewGitLabNotifier(ts.URL, "mytoken")
	env := &v1alpha1.Environment{
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "my/repo",
				MR:       42,
			},
		},
	}
	notifier.setCommentID(env, 12345)

	err := notifier.postOrUpdateComment(context.Background(), env, "updated test message")
	assert.NoError(t, err)
}

func TestGitHubNotifierPostComment(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/repos/owner/repo/issues/42/comments", r.URL.Path)
		assert.Equal(t, "Bearer mytoken", r.Header.Get("Authorization"))

		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		require.NoError(t, json.Unmarshal(body, &payload))
		assert.Contains(t, payload["body"], "test message")

		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 54321}`))
	}))
	defer ts.Close()

	notifier := NewGitHubNotifier(ts.URL, "mytoken")
	env := &v1alpha1.Environment{
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}

	err := notifier.postOrUpdateComment(context.Background(), env, "test message")
	assert.NoError(t, err)
	assert.Equal(t, 54321, env.Status.CommentID)
}

func TestNoopNotifier(t *testing.T) {
	n := &NoopNotifier{}
	ctx := context.Background()
	env := &v1alpha1.Environment{}

	assert.NoError(t, n.PostEnvironmentCreated(ctx, env))
	assert.NoError(t, n.PostEnvironmentReady(ctx, env))
	assert.NoError(t, n.PostEnvironmentFailed(ctx, env, "reason"))
	assert.NoError(t, n.PostEnvironmentTeardown(ctx, env))
	assert.NoError(t, n.UpdateEnvironmentStatus(ctx, env))
}

func TestCommentIDAnnotation(t *testing.T) {
	env := &v1alpha1.Environment{}
	g := &GitLabNotifier{}
	assert.Equal(t, 0, g.getCommentID(env))

	g.setCommentID(env, 123)
	assert.Equal(t, 123, g.getCommentID(env))

	// Both notifiers now share env.Status.CommentID (migrated from annotations)
	gh := &GitHubNotifier{}
	assert.Equal(t, 123, gh.getCommentID(env), "GitHub should see the same CommentID set by GitLab")

	gh.setCommentID(env, 456)
	assert.Equal(t, 456, gh.getCommentID(env))
	assert.Equal(t, 456, g.getCommentID(env), "GitLab should see the CommentID set by GitHub")
}

func TestGitHubNotifierProjectPathSplit(t *testing.T) {
	// Tests that the owner/repo path splitting in postOrUpdateComment
	// correctly escapes segments while preserving the separator.
	tests := []struct {
		name       string
		project    string
		expectErr  string // if non-empty, expect this error substring
		expectPath string // if no error, expect this request path
	}{
		{
			name:       "simple owner/repo",
			project:    "owner/repo",
			expectPath: "/repos/owner/repo/issues/42/comments",
		},
		{
			name:       "hyphenated org and repo",
			project:    "my-org/my-repo",
			expectPath: "/repos/my-org/my-repo/issues/42/comments",
		},
		{
			name:       "repo with dots",
			project:    "owner/my.repo.v2",
			expectPath: "/repos/owner/my.repo.v2/issues/42/comments",
		},
		{
			name:       "repo with special chars gets encoded",
			project:    "owner/repo name",
			expectPath: "/repos/owner/repo%20name/issues/42/comments",
		},
		{
			name:      "missing repo segment",
			project:   "just-a-slug",
			expectErr: "invalid project format",
		},
		{
			name:      "empty string",
			project:   "",
			expectErr: "invalid project format",
		},
		{
			name:      "empty owner",
			project:   "/repo",
			expectErr: "invalid project format",
		},
		{
			name:      "empty repo",
			project:   "owner/",
			expectErr: "invalid project format",
		},
		{
			name:      "only slash",
			project:   "/",
			expectErr: "invalid project format",
		},
		{
			name:      "path traversal attempt in owner",
			project:   "../admin/secrets",
			expectErr: "path traversal not allowed",
		},
		{
			name:       "path traversal attempt in repo",
			project:    "owner/../../etc/passwd",
			expectPath: "/repos/owner/..%2F..%2Fetc%2Fpasswd/issues/42/comments",
		},
		{
			name:      "dot-dot as repo",
			project:   "owner/..",
			expectErr: "path traversal not allowed",
		},
		{
			name:      "dot as owner",
			project:   "./repo",
			expectErr: "path traversal not allowed",
		},
		{
			name:       "triple segments treated as owner / rest",
			project:    "org/sub/repo",
			expectPath: "/repos/org/sub%2Frepo/issues/42/comments",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedURI string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedURI = r.RequestURI
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id": 1}`))
			}))
			defer ts.Close()

			notifier := NewGitHubNotifier(ts.URL, "token")
			env := &v1alpha1.Environment{
				Spec: v1alpha1.EnvironmentSpec{
					Source: v1alpha1.EnvironmentSource{
						Provider: "github",
						Project:  tt.project,
						MR:       42,
					},
				},
			}

			err := notifier.postOrUpdateComment(context.Background(), env, "test")

			if tt.expectErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectPath, receivedURI,
				"request URI for project %q", tt.project)
		})
	}
}

func TestGitHubNotifierUpdateWithSplitProject(t *testing.T) {
	// Verify that PATCH (update) path also uses the split/escaped project
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		assert.Equal(t, "/repos/my-org/my-repo/issues/comments/999", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 999}`))
	}))
	defer ts.Close()

	notifier := NewGitHubNotifier(ts.URL, "token")
	env := &v1alpha1.Environment{
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "my-org/my-repo",
				MR:       10,
			},
		},
	}
	notifier.setCommentID(env, 999)

	err := notifier.postOrUpdateComment(context.Background(), env, "update test")
	assert.NoError(t, err)
}

func TestPostEnvironmentReadyRichCommentAndCustomHeaderValue(t *testing.T) {
	var postedBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		require.NoError(t, json.Unmarshal(body, &payload))
		postedBody = payload["body"]
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 777}`))
	}))
	defer ts.Close()

	notifier := NewGitHubNotifier(ts.URL, "token")
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-42",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "my-org/my-repo",
				MR:       42,
				Branch:   "feat/checkout",
			},
			Deploy: v1alpha1.EnvironmentDeploy{
				Mode:            "delta",
				ChangedServices: []string{"checkout-api", "payment-worker"},
			},
			Routing: v1alpha1.EnvironmentRouting{
				Mode:        "header",
				HeaderKey:   "x-custom-preview",
				HeaderValue: "custom-val-42",
				AsyncRoutes: []v1alpha1.AsyncRouteSpec{
					{Protocol: "redis", Target: "cache"},
					{Protocol: "s3", Target: "uploads"},
				},
			},
			Database: v1alpha1.EnvironmentDatabase{
				Mode:       "schema",
				SeedSource: "prod-snap",
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			Phase:          v1alpha1.PhaseRunning,
			URL:            "https://preview.example.com",
			DatabaseStatus: "Ready",
			AsyncEnvVars: map[string]string{
				"DIVERGE_REDIS_PREFIX":   "pr-42:",
				"DIVERGE_STORAGE_PREFIX": "previews/pr-42/",
			},
			Conditions: []metav1.Condition{
				{
					Type:    "BaselineDriftDetected",
					Status:  metav1.ConditionTrue,
					Message: "Baseline deployment checkout-api updated to v2",
				},
			},
		},
	}

	require.NoError(t, notifier.PostEnvironmentReady(context.Background(), env))
	assert.Equal(t, 777, env.Status.CommentID)

	// Custom HeaderValue rendered in Magic Preview Link and curl -H
	assert.Contains(t, postedBody, "https://preview.example.com?_diverge_env=custom-val-42")
	assert.Contains(t, postedBody, `curl -H "x-custom-preview: custom-val-42" https://preview.example.com`)
	assert.Contains(t, postedBody, "`x-custom-preview: custom-val-42`")

	// Rich table summaries
	assert.Contains(t, postedBody, "| **Database** | `schema` (seed: prod-snap) (Ready) |")
	assert.Contains(t, postedBody, "| **Async / Storage** |")
	assert.Contains(t, postedBody, "`redis:cache`")
	assert.Contains(t, postedBody, "`s3:uploads`")
	assert.Contains(t, postedBody, "`DIVERGE_REDIS_PREFIX=pr-42:`")
	assert.Contains(t, postedBody, "`DIVERGE_STORAGE_PREFIX=previews/pr-42/`")
	assert.Contains(t, postedBody, "| **Est. Cost** |")
	assert.Contains(t, postedBody, "/hr (active)")

	// Baseline drift warning callout
	assert.Contains(t, postedBody, "**Baseline Drift Detected:** Baseline deployment checkout-api updated to v2")
	assert.Contains(t, postedBody, "kubectl annotate env pr-42 divergedev.com/drift-policy=sync --overwrite")

	// Copy-paste CLI commands
	assert.Contains(t, postedBody, "diverge verify pr-42 --compare-baseline")
	assert.Contains(t, postedBody, "diverge open pr-42 --magic")
}

func TestGitHubNotifierUpdateEnvironmentStatusUpdatesInPlace(t *testing.T) {
	var methods []string
	var paths []string
	var lastBody string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		require.NoError(t, json.Unmarshal(body, &payload))
		lastBody = payload["body"]
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 888}`))
	}))
	defer ts.Close()

	notifier := NewGitHubNotifier(ts.URL, "token")
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-88",
			Namespace: "default",
			Annotations: map[string]string{
				metrics.AnnotationHibernated: "true",
			},
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "my-org/my-repo",
				MR:       88,
				Branch:   "feat/hibernation",
			},
			Deploy: v1alpha1.EnvironmentDeploy{
				Mode:            "delta",
				ChangedServices: []string{"api"},
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			Phase:     v1alpha1.PhaseRunning,
			URL:       "https://preview.example.com",
			CommentID: 888,
		},
	}

	require.NoError(t, notifier.UpdateEnvironmentStatus(context.Background(), env))
	require.Equal(t, []string{http.MethodPatch}, methods)
	require.Equal(t, []string{"/repos/my-org/my-repo/issues/comments/888"}, paths)
	assert.Contains(t, lastBody, "💤 Hibernated ($0.000/hr)")
}
