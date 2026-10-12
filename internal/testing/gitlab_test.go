package testing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGitLabPipelineRunner_Trigger_Scenarios(t *testing.T) {
	t.Run("nil testing spec returns error", func(t *testing.T) {
		r := &GitLabPipelineRunner{BaseURL: "https://gitlab.com", Token: "gl-token", HTTPClient: http.DefaultClient}
		_, err := r.Trigger(context.Background(), &v1alpha1.Environment{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "testing spec not configured")
	})

	t.Run("successful 201 trigger returns pipeline ID", func(t *testing.T) {
		var receivedEscapedPath string
		var receivedToken string
		var receivedPayload map[string]interface{}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			receivedEscapedPath = req.URL.EscapedPath()
			receivedToken = req.Header.Get("PRIVATE-TOKEN")
			_ = json.NewDecoder(req.Body).Decode(&receivedPayload)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":54321,"web_url":"https://gitlab.com/org/repo/-/pipelines/54321"}`))
		}))
		defer srv.Close()

		r := &GitLabPipelineRunner{
			BaseURL:    srv.URL + "/",
			Token:      "gl-token",
			HTTPClient: srv.Client(),
		}
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "pr-99"},
			Spec: v1alpha1.EnvironmentSpec{
				Source: v1alpha1.EnvironmentSource{Project: "org/repo"},
				Testing: &v1alpha1.TestingSpec{
					Enabled: true,
					Trigger: v1alpha1.TestTriggerSpec{
						Type: v1alpha1.TestTriggerGitLabPipeline,
					},
				},
			},
			Status: v1alpha1.EnvironmentStatus{
				URL: "https://preview.example.com",
			},
		}

		runID, err := r.Trigger(context.Background(), env)
		require.NoError(t, err)
		assert.Equal(t, "54321", runID)
		assert.Equal(t, "/api/v4/projects/org%2Frepo/pipeline", receivedEscapedPath)
		assert.Equal(t, "gl-token", receivedToken)
		assert.Equal(t, "main", receivedPayload["ref"])
	})

	t.Run("non-201 status returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer srv.Close()

		r := &GitLabPipelineRunner{
			BaseURL:    srv.URL,
			Token:      "gl-token",
			HTTPClient: srv.Client(),
		}
		env := &v1alpha1.Environment{
			Spec: v1alpha1.EnvironmentSpec{
				Testing: &v1alpha1.TestingSpec{
					Enabled: true,
					Trigger: v1alpha1.TestTriggerSpec{
						Project: "org/repo",
						Ref:     "develop",
					},
				},
			},
		}

		_, err := r.Trigger(context.Background(), env)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected status 400")
	})
}

func TestGitLabPipelineRunner_Status_Scenarios(t *testing.T) {
	cases := []struct {
		name          string
		pipelineState string
		expectedState v1alpha1.TestState
		expectedSum   string
	}{
		{"success", "success", v1alpha1.TestStatePassed, "Pipeline passed"},
		{"failed", "failed", v1alpha1.TestStateFailed, "Pipeline failed"},
		{"canceled", "canceled", v1alpha1.TestStateFailed, "Pipeline canceled"},
		{"running", "running", v1alpha1.TestStateRunning, "Pipeline running"},
		{"pending", "pending", v1alpha1.TestStateRunning, "Pipeline pending"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				assert.Equal(t, "/api/v4/projects/org%2Frepo/pipelines/54321", req.URL.EscapedPath())
				assert.Equal(t, "gl-token", req.Header.Get("PRIVATE-TOKEN"))
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"status":  tc.pipelineState,
					"web_url": "https://gitlab.com/org/repo/-/pipelines/54321",
				})
			}))
			defer srv.Close()

			r := &GitLabPipelineRunner{BaseURL: srv.URL, Token: "gl-token", HTTPClient: srv.Client()}
			env := &v1alpha1.Environment{
				Spec: v1alpha1.EnvironmentSpec{
					Source:  v1alpha1.EnvironmentSource{Project: "org/repo"},
					Testing: &v1alpha1.TestingSpec{},
				},
			}

			res, err := r.Status(context.Background(), env, "54321")
			require.NoError(t, err)
			assert.Equal(t, tc.expectedState, res.State)
			assert.Equal(t, tc.expectedSum, res.Summary)
			assert.Equal(t, "https://gitlab.com/org/repo/-/pipelines/54321", res.URL)
		})
	}

	t.Run("HTTP error returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		r := &GitLabPipelineRunner{BaseURL: srv.URL, Token: "gl-token", HTTPClient: srv.Client()}
		env := &v1alpha1.Environment{
			Spec: v1alpha1.EnvironmentSpec{
				Source:  v1alpha1.EnvironmentSource{Project: "org/repo"},
				Testing: &v1alpha1.TestingSpec{},
			},
		}

		_, err := r.Status(context.Background(), env, "54321")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected status 500")
	})
}
