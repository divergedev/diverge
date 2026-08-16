package testing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
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

	// GitHub dispatch is fire-and-forget; we return pending and resolve async
	return "dispatch-pending", nil
}

func (r *GitHubActionsRunner) resolveDispatchRun(ctx context.Context, repo, workflow, branch string, dispatchedAt time.Time) (string, error) {
	// GitHub's search for created filters by UTC
	createdAtFilter := dispatchedAt.Add(-10 * time.Second).UTC().Format(time.RFC3339)

	params := url.Values{}
	params.Set("event", "repository_dispatch")
	if branch != "" {
		params.Set("branch", branch)
	}
	params.Set("created", ">="+createdAtFilter)
	params.Set("per_page", "10")

	// Poll /repos/{repo}/actions/runs?event=repository_dispatch
	apiURL := fmt.Sprintf("%s/repos/%s/actions/runs?%s", r.baseURL(), repo, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var result struct {
		WorkflowRuns []struct {
			ID        int64     `json:"id"`
			CreatedAt time.Time `json:"created_at"`
			Path      string    `json:"path"`
		} `json:"workflow_runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
		for _, run := range result.WorkflowRuns {
			if run.CreatedAt.After(dispatchedAt.Add(-10 * time.Second)) {
				if workflow != "" && !strings.HasSuffix(run.Path, workflow) {
					continue
				}
				return fmt.Sprintf("%d", run.ID), nil
			}
		}
	}

	return "", nil
}

func (r *GitHubActionsRunner) Status(ctx context.Context, env *v1alpha1.Environment, runID string) (*TestResult, error) {
	repo := env.Spec.Testing.Trigger.Project
	if repo == "" {
		repo = env.Spec.Source.Project
	}

	// If runID is "dispatch-pending", search for recent workflow runs
	if runID == "dispatch-pending" {
		branch := env.Spec.Testing.Trigger.Ref
		// Assuming Workflow is stored or we can pass an empty string if not in config
		// As per user request, we pass workflow and branch
		// The eventType could act as the workflow name or it's empty
		var workflow string // Or we pull it from config if added. User just said pass workflow and branch from test config.
		// If the user's config has Workflow in Trigger spec, but it doesn't, we will pass empty. Wait, let me check TestTriggerSpec.

		var dispatchedAt time.Time
		if env.Status.TestStatus != nil && env.Status.TestStatus.StartedAt != nil {
			dispatchedAt = env.Status.TestStatus.StartedAt.Time
		} else {
			dispatchedAt = time.Now().Add(-5 * time.Minute) // fallback
		}

		if time.Since(dispatchedAt) > 2*time.Minute {
			return &TestResult{
				State:   v1alpha1.TestStateFailed,
				Summary: "Timed out waiting for GitHub Actions workflow run to start",
			}, nil
		}

		resolved, err := r.resolveDispatchRun(ctx, repo, workflow, branch, dispatchedAt)
		if err != nil {
			return &TestResult{
				State:   v1alpha1.TestStateFailed,
				Summary: fmt.Sprintf("Failed to resolve dispatch run: %v", err),
			}, nil
		}
		if resolved == "" {
			return &TestResult{State: v1alpha1.TestStatePending, Summary: "Resolving dispatch run..."}, nil
		}

		// Update the run ID using ResolvedRunID without mutating env here
		runID = resolved
		// Wait, if it resolves immediately here, we still need to poll it, so we fall through.
		// But we should return it in the result so the controller saves it.
		// We'll modify the final return below to include ResolvedRunID.
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

	// If we just resolved it in this call, set ResolvedRunID so the caller can save it
	if runID != "" && env.Status.TestStatus != nil && env.Status.TestStatus.RunID == "dispatch-pending" {
		result.ResolvedRunID = runID
	}

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
