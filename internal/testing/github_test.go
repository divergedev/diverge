package testing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGitHubActionsRunner_Trigger_Scenarios(t *testing.T) {
	t.Run("nil testing spec returns error", func(t *testing.T) {
		r := &GitHubActionsRunner{Token: "gh-token", HTTPClient: http.DefaultClient}
		env := &v1alpha1.Environment{}
		_, err := r.Trigger(context.Background(), env)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "testing spec not configured")
	})

	t.Run("successful 204 dispatch with defaults returns dispatch-pending", func(t *testing.T) {
		var receivedPath string
		var receivedAuth string
		var receivedPayload map[string]interface{}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			receivedPath = req.URL.Path
			receivedAuth = req.Header.Get("Authorization")
			_ = json.NewDecoder(req.Body).Decode(&receivedPayload)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		r := &GitHubActionsRunner{
			BaseURL:    srv.URL + "/",
			Token:      "gh-token",
			HTTPClient: srv.Client(),
		}
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "pr-42"},
			Spec: v1alpha1.EnvironmentSpec{
				Source: v1alpha1.EnvironmentSource{Project: "org/source-repo"},
				Testing: &v1alpha1.TestingSpec{
					Enabled: true,
					Trigger: v1alpha1.TestTriggerSpec{
						Type: v1alpha1.TestTriggerGitHubDispatch,
					},
				},
			},
			Status: v1alpha1.EnvironmentStatus{
				URL: "https://preview.example.com",
			},
		}

		runID, err := r.Trigger(context.Background(), env)
		require.NoError(t, err)
		assert.Equal(t, "dispatch-pending", runID)
		assert.Equal(t, "/repos/org/source-repo/dispatches", receivedPath)
		assert.Equal(t, "Bearer gh-token", receivedAuth)
		assert.Equal(t, "diverge-test", receivedPayload["event_type"])

		clientPayload, ok := receivedPayload["client_payload"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "https://preview.example.com", clientPayload["diverge_url"])
		assert.Equal(t, "x-diverge-env", clientPayload["diverge_header_key"])
		assert.Equal(t, "pr-42", clientPayload["diverge_header_value"])
		assert.Equal(t, "pr-42", clientPayload["diverge_env_name"])
	})

	t.Run("non-204 status returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
		}))
		defer srv.Close()

		r := &GitHubActionsRunner{
			BaseURL:    srv.URL,
			Token:      "gh-token",
			HTTPClient: srv.Client(),
		}
		env := &v1alpha1.Environment{
			Spec: v1alpha1.EnvironmentSpec{
				Testing: &v1alpha1.TestingSpec{
					Enabled: true,
					Trigger: v1alpha1.TestTriggerSpec{
						Project:   "org/test-repo",
						EventType: "custom-e2e",
					},
				},
			},
		}

		_, err := r.Trigger(context.Background(), env)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected status 422")
	})
}

func TestGitHubActionsRunner_Status_Scenarios(t *testing.T) {
	now := time.Now()
	startedAt := metav1.NewTime(now.Add(-15 * time.Second))

	makeEnv := func() *v1alpha1.Environment {
		return &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "pr-42"},
			Spec: v1alpha1.EnvironmentSpec{
				Source: v1alpha1.EnvironmentSource{Project: "org/repo"},
				Testing: &v1alpha1.TestingSpec{
					Enabled: true,
					Trigger: v1alpha1.TestTriggerSpec{
						Ref:      "main",
						Workflow: "e2e.yml",
					},
				},
			},
			Status: v1alpha1.EnvironmentStatus{
				TestStatus: &v1alpha1.TestStatus{
					RunID:     "dispatch-pending",
					StartedAt: &startedAt,
				},
			},
		}
	}

	t.Run("dispatch-pending resolves run ID and returns completed success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch req.URL.Path {
			case "/repos/org/repo/actions/runs":
				assert.Equal(t, "repository_dispatch", req.URL.Query().Get("event"))
				assert.Equal(t, "main", req.URL.Query().Get("branch"))
				resp := map[string]interface{}{
					"workflow_runs": []map[string]interface{}{
						{
							"id":         98765,
							"created_at": now.UTC().Format(time.RFC3339),
							"path":       ".github/workflows/e2e.yml",
						},
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
			case "/repos/org/repo/actions/runs/98765":
				resp := map[string]interface{}{
					"status":     "completed",
					"conclusion": "success",
					"html_url":   "https://github.com/org/repo/actions/runs/98765",
				}
				_ = json.NewEncoder(w).Encode(resp)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		r := &GitHubActionsRunner{BaseURL: srv.URL, Token: "gh-token", HTTPClient: srv.Client()}
		res, err := r.Status(context.Background(), makeEnv(), "dispatch-pending")
		require.NoError(t, err)
		assert.Equal(t, v1alpha1.TestStatePassed, res.State)
		assert.Equal(t, "98765", res.ResolvedRunID)
		assert.Equal(t, "Workflow passed", res.Summary)
		assert.Equal(t, "https://github.com/org/repo/actions/runs/98765", res.URL)
	})

	t.Run("numeric run ID in_progress, failure, and cancelled conclusions", func(t *testing.T) {
		cases := []struct {
			name          string
			status        string
			conclusion    string
			expectedState v1alpha1.TestState
			expectedSum   string
		}{
			{"in_progress", "in_progress", "", v1alpha1.TestStateRunning, "Workflow in_progress"},
			{"failure", "completed", "failure", v1alpha1.TestStateFailed, "Workflow failed"},
			{"cancelled", "completed", "cancelled", v1alpha1.TestStateFailed, "Workflow cancelled"},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					assert.Equal(t, "/repos/org/repo/actions/runs/12345", req.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]string{
						"status":     tc.status,
						"conclusion": tc.conclusion,
						"html_url":   "https://github.com/org/repo/actions/runs/12345",
					})
				}))
				defer srv.Close()

				r := &GitHubActionsRunner{BaseURL: srv.URL, Token: "gh-token", HTTPClient: srv.Client()}
				env := makeEnv()
				env.Status.TestStatus.RunID = "12345"

				res, err := r.Status(context.Background(), env, "12345")
				require.NoError(t, err)
				assert.Equal(t, tc.expectedState, res.State)
				assert.Equal(t, tc.expectedSum, res.Summary)
			})
		}
	})
}
