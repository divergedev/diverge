package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// GitLabPipelineRunner triggers and polls GitLab CI pipelines.
type GitLabPipelineRunner struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

var _ TestRunner = (*GitLabPipelineRunner)(nil)

func (r *GitLabPipelineRunner) Trigger(ctx context.Context, env *v1alpha1.Environment) (string, error) {
	if env.Spec.Testing == nil {
		return "", fmt.Errorf("testing spec not configured")
	}

	project := env.Spec.Testing.Trigger.Project
	if project == "" {
		project = env.Spec.Source.Project
	}
	ref := env.Spec.Testing.Trigger.Ref
	if ref == "" {
		ref = "main"
	}

	// Build trigger request
	apiURL := fmt.Sprintf("%s/api/v4/projects/%s/pipeline",
		strings.TrimRight(r.BaseURL, "/"),
		url.PathEscape(project),
	)

	payload := map[string]interface{}{
		"ref": ref,
		"variables": []map[string]string{
			{"key": "DIVERGE_URL", "value": env.Status.URL},
			{"key": "DIVERGE_HEADER_KEY", "value": headerKey(env)},
			{"key": "DIVERGE_HEADER_VALUE", "value": headerValue(env)},
			{"key": "DIVERGE_ENV_NAME", "value": env.Name},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal pipeline payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("failed to create trigger request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PRIVATE-TOKEN", r.Token)

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to trigger pipeline: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("unexpected status %d triggering pipeline", resp.StatusCode)
	}

	var result struct {
		ID     int    `json:"id"`
		WebURL string `json:"web_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode pipeline response: %w", err)
	}

	return fmt.Sprintf("%d", result.ID), nil
}

func (r *GitLabPipelineRunner) Status(ctx context.Context, env *v1alpha1.Environment, runID string) (*TestResult, error) {
	project := env.Spec.Testing.Trigger.Project
	if project == "" {
		project = env.Spec.Source.Project
	}

	apiURL := fmt.Sprintf("%s/api/v4/projects/%s/pipelines/%s",
		strings.TrimRight(r.BaseURL, "/"),
		url.PathEscape(project),
		runID,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create status request: %w", err)
	}
	req.Header.Set("PRIVATE-TOKEN", r.Token)

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to poll pipeline status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d polling pipeline", resp.StatusCode)
	}

	var pipeline struct {
		Status string `json:"status"`
		WebURL string `json:"web_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pipeline); err != nil {
		return nil, fmt.Errorf("failed to decode pipeline status: %w", err)
	}

	result := &TestResult{URL: pipeline.WebURL}
	switch pipeline.Status {
	case "success":
		result.State = v1alpha1.TestStatePassed
		result.Summary = "Pipeline passed"
	case "failed":
		result.State = v1alpha1.TestStateFailed
		result.Summary = "Pipeline failed"
	case "canceled":
		result.State = v1alpha1.TestStateFailed
		result.Summary = "Pipeline canceled"
	default:
		// created, waiting_for_resource, preparing, pending, running, manual, scheduled
		result.State = v1alpha1.TestStateRunning
		result.Summary = fmt.Sprintf("Pipeline %s", pipeline.Status)
	}

	return result, nil
}
