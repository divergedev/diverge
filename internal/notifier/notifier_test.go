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
