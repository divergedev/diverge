package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type GitHubStatusReporter struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func NewGitHubStatusReporter(baseURL, token string) *GitHubStatusReporter {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &GitHubStatusReporter{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (g *GitHubStatusReporter) PostCommitStatus(ctx context.Context, env *v1alpha1.Environment, state, description string) error {
	if env.Spec.Source.Provider != "github" {
		return fmt.Errorf("environment is not from github")
	}

	sha := env.Status.CommitSHA
	if sha == "" {
		return nil
	}
	if err := validateSHA(sha); err != nil {
		return err
	}

	// Map states
	// GitHub uses pending/success/failure/error
	githubState := state
	switch state {
	case "failed":
		githubState = "failure"
	case "canceled":
		githubState = "error"
	case "running":
		githubState = "pending"
	}

	escapedProject, err := escapeProjectPath(env.Spec.Source.Project)
	if err != nil {
		return err
	}

	payload := map[string]string{
		"state":       githubState,
		"context":     "diverge/preview",
		"description": description,
	}
	if env.Status.URL != "" {
		payload["target_url"] = env.Status.URL
	}

	jsonPayload, _ := json.Marshal(payload)
	escapedSHA := url.PathEscape(sha)
	reqURL := fmt.Sprintf("%s/repos/%s/statuses/%s", g.BaseURL, escapedProject, escapedSHA)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(jsonPayload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := g.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github API returned status: %d", resp.StatusCode)
	}

	var result struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
		env.Status.CommitStatusURL = result.URL
	} else {
		env.Status.CommitStatusURL = reqURL
	}

	return nil
}
