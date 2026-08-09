package notifier_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/notifier"
)

func TestGitHubNotifierFullLifecycle(t *testing.T) {
	var requests []struct {
		Method string
		Path   string
		Header http.Header
		Body   map[string]interface{}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		if len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &body)
		}

		requests = append(requests, struct {
			Method string
			Path   string
			Header http.Header
			Body   map[string]interface{}
		}{
			Method: r.Method,
			Path:   r.URL.Path,
			Header: r.Header,
			Body:   body,
		})

		if r.Method == http.MethodPost {
			// return a comment ID
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 123}`))
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	n := notifier.NewGitHubNotifier(ts.URL, "secret-token")
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-env",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "org/repo",
				MR:       42, // GitHub PRs use MR field in diverge
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			CreatedAt: &metav1.Time{Time: time.Now()},
		},
	}

	ctx := context.Background()

	// PostEnvironmentCreated -> POST
	err := n.PostEnvironmentCreated(ctx, env)
	require.NoError(t, err)

	require.Len(t, requests, 1)
	assert.Equal(t, http.MethodPost, requests[0].Method)
	assert.Equal(t, "/repos/org/repo/issues/42/comments", requests[0].Path)
	assert.Equal(t, "Bearer secret-token", requests[0].Header.Get("Authorization"))
	assert.Equal(t, "application/json", requests[0].Header.Get("Content-Type"))
	assert.Equal(t, "application/vnd.github.v3+json", requests[0].Header.Get("Accept"))
	assert.Contains(t, requests[0].Body["body"], "test-env")

	// check if comment ID was set
	assert.Equal(t, "123", env.Annotations["diverge.io/github-pr-comment-id"])

	// GitHubNotifier.UpdateEnvironmentStatus returns nil immediately without doing anything.
	err = n.UpdateEnvironmentStatus(ctx, env)
	require.NoError(t, err)

	// Still 1 request because UpdateEnvironmentStatus does nothing
	require.Len(t, requests, 1)

	// PostEnvironmentReady -> PATCH
	err = n.PostEnvironmentReady(ctx, env)
	require.NoError(t, err)

	require.Len(t, requests, 2)
	assert.Equal(t, http.MethodPatch, requests[1].Method)
	assert.Equal(t, "/repos/org/repo/issues/comments/123", requests[1].Path)

	// PostEnvironmentTeardown -> PATCH
	err = n.PostEnvironmentTeardown(ctx, env)
	require.NoError(t, err)

	require.Len(t, requests, 3)
	assert.Equal(t, http.MethodPatch, requests[2].Method)
}

func TestGitHubNotifierAPIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	n := notifier.NewGitHubNotifier(ts.URL, "secret-token")
	env := &v1alpha1.Environment{
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "org/repo",
				MR:       42,
			},
		},
	}

	err := n.PostEnvironmentCreated(context.Background(), env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "github API returned status: 500")
}

func TestGitHubNotifierNonGitHubProvider(t *testing.T) {
	n := notifier.NewGitHubNotifier("http://localhost", "secret-token")
	env := &v1alpha1.Environment{
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
			},
		},
	}

	err := n.PostEnvironmentCreated(context.Background(), env)
	require.Error(t, err)
	assert.Equal(t, "environment is not from github", err.Error())
}
