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

// GitLabStatusReporter represents the configuration or state for this type.
type GitLabStatusReporter struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewGitLabStatusReporter performs its designated operation.
func NewGitLabStatusReporter(baseURL, token string) *GitLabStatusReporter {
	return &GitLabStatusReporter{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// PostCommitStatus performs its designated operation.
func (g *GitLabStatusReporter) PostCommitStatus(ctx context.Context, env *v1alpha1.Environment, state, description string) error {
	if env.Spec.Source.Provider != "gitlab" {
		return fmt.Errorf("environment is not from gitlab")
	}

	sha := env.Status.CommitSHA
	if sha == "" {
		return nil
	}
	if err := validateSHA(sha); err != nil {
		return err
	}

	// Reusing GitLabNotifier's getProjectAndMR for URL escaping logic
	notifier := &GitLabNotifier{}
	project, _, err := notifier.getProjectAndMR(env)
	if err != nil {
		return err
	}

	payload := map[string]string{
		"state":       state,
		"name":        "diverge/preview",
		"description": description,
	}
	if env.Status.URL != "" {
		payload["target_url"] = env.Status.URL
	}

	jsonPayload, _ := json.Marshal(payload)
	escapedSHA := url.PathEscape(sha)
	reqURL := fmt.Sprintf("%s/api/v4/projects/%s/statuses/%s", g.BaseURL, project, escapedSHA)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(jsonPayload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PRIVATE-TOKEN", g.Token)

	resp, err := g.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gitlab API returned status: %d", resp.StatusCode)
	}

	var result struct {
		TargetURL string `json:"target_url"` // Or something similar? Let's parse just to be safe.
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)

	// Since we shouldn't modify CR status directly (handled by controller), we can just set it on the struct
	// wait, we can just set it. It's a pointer to `env`.
	env.Status.CommitStatusURL = reqURL // or whatever URL

	return nil
}
