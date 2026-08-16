package testing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func testEnv() *v1alpha1.Environment {
	return &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"},
		Spec: v1alpha1.EnvironmentSpec{
			Source:  v1alpha1.EnvironmentSource{Project: "org/repo"},
			Routing: v1alpha1.EnvironmentRouting{HeaderKey: "x-diverge-env"},
			Testing: &v1alpha1.TestingSpec{
				Enabled: true,
				Trigger: v1alpha1.TestTriggerSpec{
					Type:    v1alpha1.TestTriggerGitLabPipeline,
					Project: "org/e2e-tests",
					Ref:     "main",
				},
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			URL: "https://preview.example.com",
		},
	}
}

func TestNoopTestRunner(t *testing.T) {
	r := &NoopTestRunner{}
	runID, err := r.Trigger(context.Background(), testEnv())
	require.NoError(t, err)
	assert.Equal(t, "", runID)

	result, err := r.Status(context.Background(), testEnv(), "")
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.TestStatePassed, result.State)
}

func TestGitLabPipelineRunner_Trigger(t *testing.T) {
	var receivedBody map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "/pipeline")
		assert.Equal(t, "test-token", r.Header.Get("PRIVATE-TOKEN"))
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": 12345, "web_url": "https://gitlab.com/pipeline/12345"})
	}))
	defer ts.Close()

	runner := &GitLabPipelineRunner{BaseURL: ts.URL, Token: "test-token", HTTPClient: ts.Client()}
	runID, err := runner.Trigger(context.Background(), testEnv())
	require.NoError(t, err)
	assert.Equal(t, "12345", runID)
	assert.Equal(t, "main", receivedBody["ref"])
}

func TestGitLabPipelineRunner_Status_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "web_url": "https://gitlab.com/pipeline/123"})
	}))
	defer ts.Close()

	runner := &GitLabPipelineRunner{BaseURL: ts.URL, Token: "tok", HTTPClient: ts.Client()}
	result, err := runner.Status(context.Background(), testEnv(), "123")
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.TestStatePassed, result.State)
}

func TestGitLabPipelineRunner_Status_Running(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "running", "web_url": "https://gitlab.com/pipeline/123"})
	}))
	defer ts.Close()

	runner := &GitLabPipelineRunner{BaseURL: ts.URL, Token: "tok", HTTPClient: ts.Client()}
	result, err := runner.Status(context.Background(), testEnv(), "123")
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.TestStateRunning, result.State)
}

func TestGitLabPipelineRunner_Status_Failed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "failed", "web_url": "https://gitlab.com/pipeline/123"})
	}))
	defer ts.Close()

	runner := &GitLabPipelineRunner{BaseURL: ts.URL, Token: "tok", HTTPClient: ts.Client()}
	result, err := runner.Status(context.Background(), testEnv(), "123")
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.TestStateFailed, result.State)
}

func TestGitHubActionsRunner_Trigger(t *testing.T) {
	var receivedBody map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.Contains(r.URL.Path, "/dispatches") {
			assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
			_ = json.NewDecoder(r.Body).Decode(&receivedBody)
			w.WriteHeader(http.StatusNoContent)
		} else if r.Method == "GET" && strings.Contains(r.URL.Path, "/actions/runs") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"workflow_runs": [
					{
						"id": 12345,
						"created_at": "` + time.Now().Add(5*time.Second).Format(time.RFC3339) + `"
					}
				]
			}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	env := testEnv()
	env.Spec.Testing.Trigger = v1alpha1.TestTriggerSpec{
		Type:      v1alpha1.TestTriggerGitHubDispatch,
		Project:   "org/e2e-tests",
		EventType: "preview-test",
	}

	runner := &GitHubActionsRunner{BaseURL: ts.URL, Token: "test-token", HTTPClient: ts.Client()}
	runID, err := runner.Trigger(context.Background(), env)
	require.NoError(t, err)
	assert.Equal(t, "dispatch-pending", runID)
	assert.Equal(t, "preview-test", receivedBody["event_type"])
}

func TestGitHubActionsRunner_Status_Completed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "completed", "conclusion": "success", "html_url": "https://github.com/runs/456",
		})
	}))
	defer ts.Close()

	runner := &GitHubActionsRunner{BaseURL: ts.URL, Token: "tok", HTTPClient: ts.Client()}
	result, err := runner.Status(context.Background(), testEnv(), "456")
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.TestStatePassed, result.State)
}

func TestGitHubActionsRunner_Status_InProgress(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "in_progress", "html_url": "https://github.com/runs/456",
		})
	}))
	defer ts.Close()

	runner := &GitHubActionsRunner{BaseURL: ts.URL, Token: "tok", HTTPClient: ts.Client()}
	result, err := runner.Status(context.Background(), testEnv(), "456")
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.TestStateRunning, result.State)
}

func TestHelpers(t *testing.T) {
	env := testEnv()
	assert.Equal(t, "x-diverge-env", headerKey(env))
	assert.Equal(t, "test-env", headerValue(env))

	env.Spec.Routing.HeaderKey = "x-custom"
	env.Spec.Routing.HeaderValue = "custom-val"
	assert.Equal(t, "x-custom", headerKey(env))
	assert.Equal(t, "custom-val", headerValue(env))
}

func TestStatus_DispatchPending_Resolves(t *testing.T) {
	dispatchedAt := time.Now().Add(-10 * time.Second)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/actions/runs") && r.URL.Query().Get("event") == "repository_dispatch" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"workflow_runs": [{"id": 1234, "created_at": "` + time.Now().Format(time.RFC3339) + `", "path": "ci.yml"}]}`))
			return
		}
		if strings.Contains(r.URL.Path, "/actions/runs/1234") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status": "completed", "conclusion": "success"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	runner := &GitHubActionsRunner{BaseURL: ts.URL, Token: "tok", HTTPClient: ts.Client()}
	env := testEnv()
	env.Spec.Testing.Trigger.Workflow = "ci.yml"
	now := metav1.NewTime(dispatchedAt)
	env.Status.TestStatus = &v1alpha1.TestStatus{RunID: "dispatch-pending", StartedAt: &now}

	res, err := runner.Status(context.Background(), env, "dispatch-pending")
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.TestStatePassed, res.State)
	assert.Equal(t, "1234", res.ResolvedRunID)
}

func TestStatus_DispatchPending_NotYetAvailable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workflow_runs": []}`))
	}))
	defer ts.Close()

	runner := &GitHubActionsRunner{BaseURL: ts.URL, Token: "tok", HTTPClient: ts.Client()}
	env := testEnv()
	now := metav1.Now()
	env.Status.TestStatus = &v1alpha1.TestStatus{RunID: "dispatch-pending", StartedAt: &now}

	res, err := runner.Status(context.Background(), env, "dispatch-pending")
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.TestStatePending, res.State)
}

func TestStatus_DispatchPending_Timeout(t *testing.T) {
	runner := &GitHubActionsRunner{}
	env := testEnv()
	oldTime := metav1.NewTime(time.Now().Add(-3 * time.Minute))
	env.Status.TestStatus = &v1alpha1.TestStatus{RunID: "dispatch-pending", StartedAt: &oldTime}

	res, err := runner.Status(context.Background(), env, "dispatch-pending")
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.TestStateFailed, res.State)
	assert.Contains(t, res.Summary, "Timed out waiting")
}

func TestStatus_DispatchPending_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	runner := &GitHubActionsRunner{BaseURL: ts.URL, Token: "tok", HTTPClient: ts.Client()}
	env := testEnv()
	now := metav1.Now()
	env.Status.TestStatus = &v1alpha1.TestStatus{RunID: "dispatch-pending", StartedAt: &now}

	res, err := runner.Status(context.Background(), env, "dispatch-pending")
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.TestStateFailed, res.State)
}

func TestResolveDispatchRun_FiltersWorkflow(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workflow_runs": [{"id": 1, "created_at": "` + time.Now().Format(time.RFC3339) + `", "path": "wrong.yml"}]}`))
	}))
	defer ts.Close()

	runner := &GitHubActionsRunner{BaseURL: ts.URL, Token: "tok", HTTPClient: ts.Client()}
	res, err := runner.resolveDispatchRun(context.Background(), "repo", "ci.yml", "main", time.Now().Add(-time.Minute))
	require.NoError(t, err)
	assert.Equal(t, "", res)
}

func TestResolveDispatchRun_FiltersBranch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "main", r.URL.Query().Get("branch"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workflow_runs": []}`))
	}))
	defer ts.Close()

	runner := &GitHubActionsRunner{BaseURL: ts.URL, Token: "tok", HTTPClient: ts.Client()}
	_, err := runner.resolveDispatchRun(context.Background(), "repo", "ci.yml", "main", time.Now())
	require.NoError(t, err)
}

func TestTrigger_Failure_SetsFailedState(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	runner := &GitHubActionsRunner{BaseURL: ts.URL, Token: "tok", HTTPClient: ts.Client()}
	env := testEnv()
	_, err := runner.Trigger(context.Background(), env)
	assert.ErrorContains(t, err, "unexpected status 403")
}

func TestStatus_DispatchPending_WorkflowFilter(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/actions/runs") && r.URL.Query().Get("event") == "repository_dispatch" {
			w.Header().Set("Content-Type", "application/json")
			// Return two runs: one that doesn't match the workflow, one that does
			_, _ = w.Write([]byte(`{"workflow_runs": [
				{"id": 111, "created_at": "` + time.Now().Format(time.RFC3339) + `", "path": ".github/workflows/other.yml"},
				{"id": 222, "created_at": "` + time.Now().Format(time.RFC3339) + `", "path": ".github/workflows/e2e.yml"}
			]}`))
			return
		}
		if strings.Contains(r.URL.Path, "/actions/runs/222") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status": "completed", "conclusion": "success"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	runner := &GitHubActionsRunner{BaseURL: ts.URL, Token: "tok", HTTPClient: ts.Client()}
	env := testEnv()
	env.Spec.Testing.Trigger.Workflow = "e2e.yml"
	now := metav1.Now()
	env.Status.TestStatus = &v1alpha1.TestStatus{RunID: "dispatch-pending", StartedAt: &now}

	res, err := runner.Status(context.Background(), env, "dispatch-pending")
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.TestStatePassed, res.State)
	assert.Equal(t, "222", res.ResolvedRunID)
}
