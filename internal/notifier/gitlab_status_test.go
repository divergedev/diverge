package notifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitLabStatusReporter_PostCommitStatus(t *testing.T) {
	sha := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

	t.Run("success 201 with URL encoding of org/repo and states", func(t *testing.T) {
		states := []string{"success", "failed", "running", "pending", "canceled"}
		for _, state := range states {
			t.Run(state, func(t *testing.T) {
				var receivedURI string
				var receivedToken string
				var receivedPayload map[string]string

				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					receivedURI = r.URL.EscapedPath()
					receivedToken = r.Header.Get("PRIVATE-TOKEN")
					_ = json.NewDecoder(r.Body).Decode(&receivedPayload)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"id":1,"status":"` + state + `"}`))
				}))
				defer srv.Close()

				reporter := NewGitLabStatusReporter(srv.URL, "gl-secret")
				env := &v1alpha1.Environment{
					Spec: v1alpha1.EnvironmentSpec{
						Source: v1alpha1.EnvironmentSource{
							Provider: "gitlab",
							Project:  "org/sub/repo",
						},
					},
					Status: v1alpha1.EnvironmentStatus{
						CommitSHA: sha,
						URL:       "https://preview.example.com",
					},
				}

				err := reporter.PostCommitStatus(context.Background(), env, state, "Preview environment status")
				require.NoError(t, err)
				assert.Equal(t, "/api/v4/projects/org%2Fsub%2Frepo/statuses/"+sha, receivedURI)
				assert.Equal(t, "gl-secret", receivedToken)
				assert.Equal(t, state, receivedPayload["state"])
				assert.Equal(t, "diverge/preview", receivedPayload["name"])
				assert.Equal(t, "Preview environment status", receivedPayload["description"])
				assert.Equal(t, "https://preview.example.com", receivedPayload["target_url"])
				assert.Equal(t, srv.URL+"/api/v4/projects/org%2Fsub%2Frepo/statuses/"+sha, env.Status.CommitStatusURL)
			})
		}
	})

	t.Run("missing commit SHA skips HTTP call", func(t *testing.T) {
		called := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusCreated)
		}))
		defer srv.Close()

		reporter := NewGitLabStatusReporter(srv.URL, "gl-secret")
		env := &v1alpha1.Environment{
			Spec: v1alpha1.EnvironmentSpec{
				Source: v1alpha1.EnvironmentSource{
					Provider: "gitlab",
					Project:  "org/repo",
				},
			},
		}

		err := reporter.PostCommitStatus(context.Background(), env, "success", "Ready")
		require.NoError(t, err)
		assert.False(t, called)
	})

	t.Run("non-gitlab provider returns error", func(t *testing.T) {
		reporter := NewGitLabStatusReporter("https://gitlab.com", "gl-secret")
		env := &v1alpha1.Environment{
			Spec: v1alpha1.EnvironmentSpec{
				Source: v1alpha1.EnvironmentSource{
					Provider: "github",
					Project:  "org/repo",
				},
			},
			Status: v1alpha1.EnvironmentStatus{CommitSHA: sha},
		}

		err := reporter.PostCommitStatus(context.Background(), env, "success", "Ready")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not from gitlab")
	})

	t.Run("invalid commit SHA returns error", func(t *testing.T) {
		reporter := NewGitLabStatusReporter("https://gitlab.com", "gl-secret")
		env := &v1alpha1.Environment{
			Spec: v1alpha1.EnvironmentSpec{
				Source: v1alpha1.EnvironmentSource{
					Provider: "gitlab",
					Project:  "org/repo",
				},
			},
			Status: v1alpha1.EnvironmentStatus{CommitSHA: "bad-sha!"},
		}

		err := reporter.PostCommitStatus(context.Background(), env, "success", "Ready")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid commit SHA")
	})

	t.Run("HTTP error status returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer srv.Close()

		reporter := NewGitLabStatusReporter(srv.URL, "gl-secret")
		env := &v1alpha1.Environment{
			Spec: v1alpha1.EnvironmentSpec{
				Source: v1alpha1.EnvironmentSource{
					Provider: "gitlab",
					Project:  "org/repo",
				},
			},
			Status: v1alpha1.EnvironmentStatus{CommitSHA: sha},
		}

		err := reporter.PostCommitStatus(context.Background(), env, "failed", "Failed")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "gitlab API returned status: 400")
	})
}
