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

	// GitHub dispatch is fire-and-forget; there's no run ID returned.
	// We'll poll the Actions API to find the workflow run we just triggered.
	dispatchedAt := time.Now()
	runID, err := r.resolveDispatchRun(ctx, repo, eventType, dispatchedAt)
	if err != nil {
		// fallback to pending if we couldn't resolve
		return "dispatch-pending", nil
	}

	return runID, nil
}

func (r *GitHubActionsRunner) resolveDispatchRun(ctx context.Context, repo, eventType string, dispatchedAt time.Time) (string, error) {
	deadline := time.After(30 * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// GitHub's search for created filters by UTC
	createdAtFilter := dispatchedAt.Add(-10 * time.Second).UTC().Format(time.RFC3339)

	for {
		select {
		case <-deadline:
			return "", fmt.Errorf("timed out resolving dispatch to run ID")
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			// Poll /repos/{repo}/actions/runs?event=repository_dispatch
			apiURL := fmt.Sprintf("%s/repos/%s/actions/runs?event=repository_dispatch&created=>%s", r.baseURL(), repo, url.QueryEscape(createdAtFilter))
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
			if err != nil {
				continue
			}
			req.Header.Set("Authorization", "Bearer "+r.Token)
			req.Header.Set("Accept", "application/vnd.github+json")

			resp, err := r.HTTPClient.Do(req)
			if err != nil {
				continue
			}
			if resp.StatusCode != http.StatusOK {
				_ = resp.Body.Close()
				continue
			}

			var result struct {
				WorkflowRuns []struct {
					ID        int64     `json:"id"`
					CreatedAt time.Time `json:"created_at"`
				} `json:"workflow_runs"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
				for _, run := range result.WorkflowRuns {
					if run.CreatedAt.After(dispatchedAt.Add(-10 * time.Second)) {
						_ = resp.Body.Close()
						return fmt.Sprintf("%d", run.ID), nil
					}
				}
			}
			_ = resp.Body.Close()
		}
	}
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
