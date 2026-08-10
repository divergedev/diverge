package notifier

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Equal(t, "12345", env.Annotations["diverge.io/gitlab-mr-comment-id"])
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
	assert.Equal(t, "54321", env.Annotations["diverge.io/github-pr-comment-id"])
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

	gh := &GitHubNotifier{}
	assert.Equal(t, 0, gh.getCommentID(env))

	gh.setCommentID(env, 456)
	assert.Equal(t, 456, gh.getCommentID(env))
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
