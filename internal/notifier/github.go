package notifier

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// GitHubNotifier implements Notifier by posting and updating issue comments
// on GitHub pull requests via the REST API.
type GitHubNotifier struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewGitHubNotifier creates a Notifier that posts and updates deployment status
// comments on GitHub pull requests via the GitHub REST API. If baseURL is empty,
// it defaults to https://api.github.com.
func NewGitHubNotifier(baseURL, token string) *GitHubNotifier {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &GitHubNotifier{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (g *GitHubNotifier) getProjectAndPR(env *v1alpha1.Environment) (string, int, error) {
	if env.Spec.Source.Provider != "github" {
		return "", 0, fmt.Errorf("environment is not from github")
	}
	return env.Spec.Source.Project, env.Spec.Source.MR, nil
}

func (g *GitHubNotifier) getCommentID(env *v1alpha1.Environment) int {
	return env.Status.CommentID
}

func (g *GitHubNotifier) setCommentID(env *v1alpha1.Environment, id int) {
	env.Status.CommentID = id
}

func (g *GitHubNotifier) postOrUpdateComment(ctx context.Context, env *v1alpha1.Environment, body string) error {
	project, pr, err := g.getProjectAndPR(env)
	if err != nil {
		return err
	}

	escapedProject, err := escapeProjectPath(project)
	if err != nil {
		return err
	}

	payload := map[string]string{"body": body}
	jsonPayload, _ := json.Marshal(payload)

	backoff := 500 * time.Millisecond
	maxRetries := 3

	for attempt := 0; attempt <= maxRetries; attempt++ {
		commentID := g.getCommentID(env)
		var reqURL string
		var method string

		if commentID != 0 {
			reqURL = fmt.Sprintf("%s/repos/%s/issues/comments/%d", g.BaseURL, escapedProject, commentID)
			method = http.MethodPatch
		} else {
			reqURL = fmt.Sprintf("%s/repos/%s/issues/%d/comments", g.BaseURL, escapedProject, pr)
			method = http.MethodPost
		}

		req, err := http.NewRequestWithContext(ctx, method, reqURL, bytes.NewReader(jsonPayload))
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

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
			_ = resp.Body.Close()
			if attempt == maxRetries {
				return fmt.Errorf("github API rate limited after retries")
			}

			sleepDuration := backoff
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				if parsedSecs, err := strconv.Atoi(retryAfter); err == nil {
					sleepDuration = time.Duration(parsedSecs) * time.Second
				}
			} else if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
				if resetUnix, err := strconv.ParseInt(reset, 10, 64); err == nil {
					resetTime := time.Unix(resetUnix, 0)
					if resetTime.After(time.Now()) {
						d := time.Until(resetTime)
						if d < 1*time.Minute {
							sleepDuration = d
						}
					}
				}
			}

			jitter := time.Duration(rand.Int63n(int64(sleepDuration/2) + 1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleepDuration + jitter):
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_ = resp.Body.Close()
			if commentID != 0 && resp.StatusCode == http.StatusNotFound {
				g.setCommentID(env, 0)
				continue
			}
			return fmt.Errorf("github API returned status: %d", resp.StatusCode)
		}

		if commentID == 0 {
			var result struct {
				ID int `json:"id"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.ID != 0 {
				g.setCommentID(env, result.ID)
			}
		}
		_ = resp.Body.Close()
		break
	}

	return nil
}

// PostEnvironmentCreated posts a comment indicating a new environment is being provisioned.
func (g *GitHubNotifier) PostEnvironmentCreated(ctx context.Context, env *v1alpha1.Environment) error {
	data := buildTemplateData(env, "")
	msg, err := renderTemplate(createdTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, env, msg)
}

// PostEnvironmentReady posts a comment indicating the environment is ready with its preview URL.
func (g *GitHubNotifier) PostEnvironmentReady(ctx context.Context, env *v1alpha1.Environment) error {
	data := buildTemplateData(env, "")
	msg, err := renderTemplate(readyTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, env, msg)
}

// PostEnvironmentFailed posts a comment indicating the environment deployment failed.
func (g *GitHubNotifier) PostEnvironmentFailed(ctx context.Context, env *v1alpha1.Environment, reason string) error {
	data := buildTemplateData(env, reason)
	msg, err := renderTemplate(failedTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, env, msg)
}

// PostEnvironmentTeardown posts a comment indicating the environment is being torn down.
func (g *GitHubNotifier) PostEnvironmentTeardown(ctx context.Context, env *v1alpha1.Environment) error {
	data := buildTemplateData(env, "Teardown requested")
	msg, err := renderTemplate(teardownTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, env, msg)
}

// UpdateEnvironmentStatus is a no-op for GitHub; status is conveyed via comments.
func (g *GitHubNotifier) UpdateEnvironmentStatus(ctx context.Context, env *v1alpha1.Environment) error {
	return nil
}

// escapeProjectPath splits an "owner/repo" project string, validates both
// segments, and returns a URL-safe path with each segment independently escaped.
// H2: Prevents API path traversal by rejecting "." and ".." segments and
// encoding special characters within each segment.
func escapeProjectPath(project string) (string, error) {
	parts := strings.SplitN(project, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("invalid project format %q: expected owner/repo", project)
	}
	if parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return "", fmt.Errorf("invalid project format %q: path traversal not allowed", project)
	}
	return url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]), nil
}

// ValidateGitHubWebhookSignature validates a GitHub webhook payload signature
// using the provided secret. The signature should be the value of the
// X-Hub-Signature-256 header (e.g. "sha256=...").
func ValidateGitHubWebhookSignature(payload []byte, signature, secret string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(strings.TrimPrefix(signature, "sha256=")), []byte(expectedMAC))
}

// GitHubPreviewGroupNotifier implements PreviewGroupNotifier for GitHub.
type GitHubPreviewGroupNotifier struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewGitHubPreviewGroupNotifier creates a GitHub-backed notifier that posts preview group status to pull request comments using the GitHub API.
func NewGitHubPreviewGroupNotifier(baseURL, token string) *GitHubPreviewGroupNotifier {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &GitHubPreviewGroupNotifier{
		BaseURL:    baseURL,
		Token:      token,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (g *GitHubPreviewGroupNotifier) getProjectAndMR(pg *v1alpha1.PreviewGroup) (string, int, error) {
	if pg.Spec.Source.Provider != "github" {
		return "", 0, fmt.Errorf("preview group source is not github")
	}
	if pg.Spec.Source.MR == 0 {
		return "", 0, ErrMissingMergeRequest
	}
	return pg.Spec.Source.Project, pg.Spec.Source.MR, nil
}

func (g *GitHubPreviewGroupNotifier) getCommentID(pg *v1alpha1.PreviewGroup) int64 {
	return pg.Status.CommentID
}

func (g *GitHubPreviewGroupNotifier) setCommentID(pg *v1alpha1.PreviewGroup, id int64) {
	pg.Status.CommentID = id
}

func (g *GitHubPreviewGroupNotifier) postOrUpdateComment(ctx context.Context, pg *v1alpha1.PreviewGroup, body string) error {
	project, mr, err := g.getProjectAndMR(pg)
	if err != nil {
		return err
	}

	escapedProject, err := escapeProjectPath(project)
	if err != nil {
		return err
	}

	payload := map[string]string{"body": body}
	jsonPayload, _ := json.Marshal(payload)

	backoff := 500 * time.Millisecond
	maxRetries := 3

	for attempt := 0; attempt <= maxRetries; attempt++ {
		commentID := g.getCommentID(pg)
		var reqURL string
		var method string

		if commentID != 0 {
			reqURL = fmt.Sprintf("%s/repos/%s/issues/comments/%d", g.BaseURL, escapedProject, commentID)
			method = http.MethodPatch
		} else {
			reqURL = fmt.Sprintf("%s/repos/%s/issues/%d/comments", g.BaseURL, escapedProject, mr)
			method = http.MethodPost
		}

		req, err := http.NewRequestWithContext(ctx, method, reqURL, bytes.NewReader(jsonPayload))
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

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
			_ = resp.Body.Close()
			if attempt == maxRetries {
				return fmt.Errorf("github API rate limited after retries")
			}

			sleepDuration := backoff
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				if parsedSecs, err := strconv.Atoi(retryAfter); err == nil {
					sleepDuration = time.Duration(parsedSecs) * time.Second
				}
			} else if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
				if resetUnix, err := strconv.ParseInt(reset, 10, 64); err == nil {
					resetTime := time.Unix(resetUnix, 0)
					if resetTime.After(time.Now()) {
						d := time.Until(resetTime)
						if d < 1*time.Minute {
							sleepDuration = d
						}
					}
				}
			}

			jitter := time.Duration(rand.Int63n(int64(sleepDuration/2) + 1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleepDuration + jitter):
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_ = resp.Body.Close()
			if commentID != 0 && resp.StatusCode == http.StatusNotFound {
				g.setCommentID(pg, 0)
				continue
			}
			return fmt.Errorf("github API returned status: %d", resp.StatusCode)
		}

		if commentID == 0 {
			var result struct {
				ID int64 `json:"id"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.ID != 0 {
				g.setCommentID(pg, result.ID)
			}
		}
		_ = resp.Body.Close()
		break
	}
	return nil
}

// PostGroupCreated performs its designated operation.
func (g *GitHubPreviewGroupNotifier) PostGroupCreated(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	data := buildPreviewGroupTemplateData(pg, "")
	msg, err := renderTemplate(pgCreatedTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}

// PostGroupReady performs its designated operation.
func (g *GitHubPreviewGroupNotifier) PostGroupReady(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	data := buildPreviewGroupTemplateData(pg, "")
	msg, err := renderTemplate(pgReadyTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}

// PostGroupFailed performs its designated operation.
func (g *GitHubPreviewGroupNotifier) PostGroupFailed(ctx context.Context, pg *v1alpha1.PreviewGroup, reason string) error {
	data := buildPreviewGroupTemplateData(pg, reason)
	msg, err := renderTemplate(pgFailedTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}

// PostGroupTeardown performs its designated operation.
func (g *GitHubPreviewGroupNotifier) PostGroupTeardown(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	data := buildPreviewGroupTemplateData(pg, "Teardown requested")
	msg, err := renderTemplate(pgTeardownTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}

// UpdateGroupStatus performs its designated operation.
func (g *GitHubPreviewGroupNotifier) UpdateGroupStatus(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	data := buildPreviewGroupTemplateData(pg, "")
	msg, err := renderTemplate(pgReadyTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}

func (g *GitHubNotifier) resolveDispatchRun(ctx context.Context, owner, repo, workflow, branch string, dispatchedAt time.Time) (int64, error) {
	createdAtFilter := dispatchedAt.Add(-10 * time.Second).UTC().Format(time.RFC3339)
	escapedProject, err := escapeProjectPath(owner + "/" + repo)
	if err != nil {
		return 0, err
	}

	params := url.Values{}
	params.Set("event", "workflow_dispatch")
	params.Set("branch", branch)
	params.Set("created", ">="+createdAtFilter)
	if workflow != "" {
		params.Set("workflow_id", workflow)
	}
	apiURL := fmt.Sprintf("%s/repos/%s/actions/runs?%s", g.BaseURL, escapedProject, params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := g.HTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("GitHub API error: %d", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var result struct {
		WorkflowRuns []struct {
			ID        int64     `json:"id"`
			CreatedAt time.Time `json:"created_at"`
			Name      string    `json:"name"`
		} `json:"workflow_runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
		for _, run := range result.WorkflowRuns {
			if run.CreatedAt.After(dispatchedAt.Add(-10 * time.Second)) {
				return run.ID, nil
			}
		}
	}

	return 0, fmt.Errorf("run not found")
}
