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

func TestGitLabNotifierFullLifecycle(t *testing.T) {
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

	n := notifier.NewGitLabNotifier(ts.URL, "secret-token")
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-env",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "org/repo",
				MR:       42,
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
	assert.Equal(t, "/api/v4/projects/org/repo/merge_requests/42/notes", requests[0].Path)
	assert.Equal(t, "secret-token", requests[0].Header.Get("PRIVATE-TOKEN"))
	assert.Equal(t, "application/json", requests[0].Header.Get("Content-Type"))
	assert.Contains(t, requests[0].Body["body"], "test-env")

	// check if comment ID was set
	assert.Equal(t, 123, env.Status.CommentID)

	// UpdateEnvironmentStatus -> PUT
	err = n.UpdateEnvironmentStatus(ctx, env)
	require.NoError(t, err)

	require.Len(t, requests, 2)
	assert.Equal(t, http.MethodPut, requests[1].Method)
	assert.Equal(t, "/api/v4/projects/org/repo/merge_requests/42/notes/123", requests[1].Path)

	// PostEnvironmentReady -> PUT
	err = n.PostEnvironmentReady(ctx, env)
	require.NoError(t, err)

	require.Len(t, requests, 3)
	assert.Equal(t, http.MethodPut, requests[2].Method)

	// PostEnvironmentTeardown -> PUT
	err = n.PostEnvironmentTeardown(ctx, env)
	require.NoError(t, err)

	require.Len(t, requests, 4)
	assert.Equal(t, http.MethodPut, requests[3].Method)
}

func TestGitLabNotifierAPIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	n := notifier.NewGitLabNotifier(ts.URL, "secret-token")
	env := &v1alpha1.Environment{
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "org/repo",
				MR:       42,
			},
		},
	}

	err := n.PostEnvironmentCreated(context.Background(), env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gitlab API returned status: 500")
}

func TestGitLabNotifierNonGitLabProvider(t *testing.T) {
	n := notifier.NewGitLabNotifier("http://localhost", "secret-token")
	env := &v1alpha1.Environment{
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
			},
		},
	}

	err := n.PostEnvironmentCreated(context.Background(), env)
	require.Error(t, err)
	assert.Equal(t, "environment is not from gitlab", err.Error())
}
