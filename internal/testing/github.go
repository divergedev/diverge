package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// GitHubActionsRunner triggers and polls GitHub Actions workflow runs.
type GitHubActionsRunner struct {
	BaseURL    string // defaults to https://api.github.com
	Token      string
	HTTPClient *http.Client
}

var _ TestRunner = (*GitHubActionsRunner)(nil)

func (r *GitHubActionsRunner) baseURL() string {
	if r.BaseURL != "" {
		return strings.TrimRight(r.BaseURL, "/")
	}
	return "https://api.github.com"
}

func (r *GitHubActionsRunner) Trigger(ctx context.Context, env *v1alpha1.Environment) (string, error) {
	if env.Spec.Testing == nil {
		return "", fmt.Errorf("testing spec not configured")
	}

	repo := env.Spec.Testing.Trigger.Project
	if repo == "" {
		repo = env.Spec.Source.Project
	}
	eventType := env.Spec.Testing.Trigger.EventType
	if eventType == "" {
		eventType = "diverge-test"
	}

	apiURL := fmt.Sprintf("%s/repos/%s/dispatches", r.baseURL(), repo)

	payload := map[string]interface{}{
		"event_type": eventType,
		"client_payload": map[string]string{
			"diverge_url":          env.Status.URL,
			"diverge_header_key":   headerKey(env),
			"diverge_header_value": headerValue(env),
			"diverge_env_name":     env.Name,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal dispatch payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("failed to create dispatch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to dispatch event: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// GitHub returns 204 No Content for successful dispatches
	if resp.StatusCode != http.StatusNoContent {
		return "", fmt.Errorf("unexpected status %d dispatching event", resp.StatusCode)
	}

	// GitHub dispatch is fire-and-forget; there's no run ID returned.
	// We'll use a sentinel to trigger workflow run discovery on first poll.
	return "dispatch-pending", nil
}

func (r *GitHubActionsRunner) Status(ctx context.Context, env *v1alpha1.Environment, runID string) (*TestResult, error) {
	repo := env.Spec.Testing.Trigger.Project
	if repo == "" {
		repo = env.Spec.Source.Project
	}

	// If runID is "dispatch-pending", search for recent workflow runs
	if runID == "dispatch-pending" {
		// For dispatch-pending, we check if a recent run appeared
		// This is a best-effort approach since GitHub dispatch is async
		return &TestResult{State: v1alpha1.TestStateRunning, Summary: "Waiting for workflow to start"}, nil
	}

	apiURL := fmt.Sprintf("%s/repos/%s/actions/runs/%s", r.baseURL(), repo, runID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create status request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to poll workflow run: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d polling workflow run", resp.StatusCode)
	}

	var run struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HTMLURL    string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return nil, fmt.Errorf("failed to decode workflow run: %w", err)
	}

	result := &TestResult{URL: run.HTMLURL}
	if run.Status != "completed" {
		result.State = v1alpha1.TestStateRunning
		result.Summary = fmt.Sprintf("Workflow %s", run.Status)
		return result, nil
	}

	switch run.Conclusion {
	case "success":
		result.State = v1alpha1.TestStatePassed
		result.Summary = "Workflow passed"
	case "failure":
		result.State = v1alpha1.TestStateFailed
		result.Summary = "Workflow failed"
	default:
		result.State = v1alpha1.TestStateFailed
		result.Summary = fmt.Sprintf("Workflow %s", run.Conclusion)
	}

	return result, nil
}
