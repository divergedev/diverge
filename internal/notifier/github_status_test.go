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

func TestNewGitHubStatusReporter_Defaults(t *testing.T) {
	r := NewGitHubStatusReporter("", "my-token")
	assert.Equal(t, "https://api.github.com", r.BaseURL)
	assert.Equal(t, "my-token", r.Token)
	assert.NotNil(t, r.HTTPClient)
}

func TestGitHubStatusReporter_PostCommitStatus(t *testing.T) {
	sha := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

	t.Run("success 201 and state mapping", func(t *testing.T) {
		stateCases := []struct {
			inputState    string
			expectedState string
		}{
			{"success", "success"},
			{"failed", "failure"},
			{"canceled", "error"},
			{"running", "pending"},
			{"pending", "pending"},
		}

		for _, tc := range stateCases {
			t.Run(tc.inputState, func(t *testing.T) {
				var receivedPayload map[string]string
				var receivedPath string
				var receivedAuth string

				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					receivedPath = r.URL.Path
					receivedAuth = r.Header.Get("Authorization")
					_ = json.NewDecoder(r.Body).Decode(&receivedPayload)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"url":"https://api.github.com/repos/org/repo/statuses/` + sha + `"}`))
				}))
				defer srv.Close()

				reporter := NewGitHubStatusReporter(srv.URL, "gh-secret")
				env := &v1alpha1.Environment{
					Spec: v1alpha1.EnvironmentSpec{
						Source: v1alpha1.EnvironmentSource{
							Provider: "github",
							Project:  "org/repo",
						},
					},
					Status: v1alpha1.EnvironmentStatus{
						CommitSHA: sha,
						URL:       "https://preview.example.com",
					},
				}

				err := reporter.PostCommitStatus(context.Background(), env, tc.inputState, "Preview status update")
				require.NoError(t, err)
				assert.Equal(t, "/repos/org/repo/statuses/"+sha, receivedPath)
				assert.Equal(t, "Bearer gh-secret", receivedAuth)
				assert.Equal(t, tc.expectedState, receivedPayload["state"])
				assert.Equal(t, "diverge/preview", receivedPayload["context"])
				assert.Equal(t, "Preview status update", receivedPayload["description"])
				assert.Equal(t, "https://preview.example.com", receivedPayload["target_url"])
				assert.Equal(t, "https://api.github.com/repos/org/repo/statuses/"+sha, env.Status.CommitStatusURL)
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

		reporter := NewGitHubStatusReporter(srv.URL, "gh-secret")
		env := &v1alpha1.Environment{
			Spec: v1alpha1.EnvironmentSpec{
				Source: v1alpha1.EnvironmentSource{
					Provider: "github",
					Project:  "org/repo",
				},
			},
		}

		err := reporter.PostCommitStatus(context.Background(), env, "success", "Ready")
		require.NoError(t, err)
		assert.False(t, called)
	})

	t.Run("non-github provider returns error", func(t *testing.T) {
		reporter := NewGitHubStatusReporter("https://api.github.com", "gh-secret")
		env := &v1alpha1.Environment{
			Spec: v1alpha1.EnvironmentSpec{
				Source: v1alpha1.EnvironmentSource{
					Provider: "gitlab",
					Project:  "org/repo",
				},
			},
			Status: v1alpha1.EnvironmentStatus{CommitSHA: sha},
		}

		err := reporter.PostCommitStatus(context.Background(), env, "success", "Ready")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not from github")
	})

	t.Run("invalid commit SHA returns error", func(t *testing.T) {
		reporter := NewGitHubStatusReporter("https://api.github.com", "gh-secret")
		env := &v1alpha1.Environment{
			Spec: v1alpha1.EnvironmentSpec{
				Source: v1alpha1.EnvironmentSource{
					Provider: "github",
					Project:  "org/repo",
				},
			},
			Status: v1alpha1.EnvironmentStatus{CommitSHA: "invalid-sha!"},
		}

		err := reporter.PostCommitStatus(context.Background(), env, "success", "Ready")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid commit SHA")
	})

	t.Run("HTTP error status returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		reporter := NewGitHubStatusReporter(srv.URL, "gh-secret")
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
		assert.Contains(t, err.Error(), "github API returned status: 500")
	})
}
