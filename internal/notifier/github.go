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
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
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

// UpdateEnvironmentStatus re-renders and updates the existing pull request comment with current status.
func (g *GitHubNotifier) UpdateEnvironmentStatus(ctx context.Context, env *v1alpha1.Environment) error {
	if env == nil || (env.Status.Phase != v1alpha1.PhaseRunning && env.Status.URL == "") {
		return nil
	}
	return g.PostEnvironmentReady(ctx, env)
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
	BaseURL     string
	Token       string
	HTTPClient  *http.Client
	AuthorLogin string

	authorCache cachedNotifierAuthor
}

// NewGitHubPreviewGroupNotifier creates a GitHub-backed notifier that posts preview group status to pull request comments using the GitHub API.
func NewGitHubPreviewGroupNotifier(baseURL, token string) *GitHubPreviewGroupNotifier {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
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

func (g *GitHubPreviewGroupNotifier) resolveAuthenticatedAuthor(ctx context.Context) (int64, string, error) {
	if id, login, needFetch := g.authorCache.get(g.AuthorLogin); !needFetch {
		return id, login, nil
	}
	baseURL := strings.TrimRight(strings.TrimSpace(g.BaseURL), "/")
	if baseURL == "" {
		return 0, "", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/user", nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	httpClient := g.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("github /user lookup failed: %w", err)
	}
	if resp.StatusCode == http.StatusForbidden &&
		(resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0") {
		_ = resp.Body.Close()
		return 0, "", fmt.Errorf("github /user lookup rate limited: status %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		g.authorCache.markChecked()
		return 0, "", nil
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return 0, "", fmt.Errorf("github /user lookup returned status: %d", resp.StatusCode)
	}
	var u struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	decErr := json.NewDecoder(resp.Body).Decode(&u)
	_ = resp.Body.Close()
	if decErr == nil && strings.TrimSpace(u.Login) != "" {
		login := strings.TrimSpace(u.Login)
		g.authorCache.record(u.ID, login)
		return u.ID, login, nil
	}
	g.authorCache.markChecked()
	return 0, "", nil
}

func (g *GitHubPreviewGroupNotifier) findExistingCommentID(ctx context.Context, escapedProject string, mr int, marker string) (int64, error) {
	if marker == "" {
		return 0, nil
	}
	authID, authLogin, err := g.resolveAuthenticatedAuthor(ctx)
	if err != nil {
		return 0, err
	}
	if authID == 0 && authLogin == "" {
		return 0, nil
	}
	httpClient := g.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	nextURL := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100", g.BaseURL, escapedProject, mr)
	var matchedID int64
	for page := 0; page < 5 && nextURL != ""; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bearer "+g.Token)
		req.Header.Set("Accept", "application/vnd.github.v3+json")
		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, fmt.Errorf("github comment lookup failed: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return 0, fmt.Errorf("github comment lookup returned status: %d", resp.StatusCode)
		}
		var comments []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
			User struct {
				ID    int64  `json:"id"`
				Login string `json:"login"`
			} `json:"user"`
		}
		decErr := json.NewDecoder(resp.Body).Decode(&comments)
		linkHeader := resp.Header.Get("Link")
		_ = resp.Body.Close()
		if decErr != nil {
			return 0, fmt.Errorf("decoding github comment lookup response: %w", decErr)
		}
		for _, c := range comments {
			if c.ID != 0 && notifierAuthorMatches(authID, authLogin, c.User.ID, c.User.Login) && strings.Contains(c.Body, marker) {
				matchedID = c.ID
			}
		}
		nextURL = parseNextLink(linkHeader)
	}
	return matchedID, nil
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

	commentID := g.getCommentID(pg)
	var recoveredID int64
	if commentID == 0 {
		existingID, lookupErr := g.findExistingCommentID(ctx, escapedProject, mr, previewGroupCommentMarker(pg))
		if lookupErr != nil {
			return lookupErr
		}
		if existingID != 0 {
			commentID = existingID
			recoveredID = existingID
		}
	}

	body = withPreviewGroupCommentMarker(pg, body)
	payload := map[string]string{"body": body}
	jsonPayload, _ := json.Marshal(payload)

	backoff := 500 * time.Millisecond
	maxRetries := 3

	for attempt := 0; attempt <= maxRetries; attempt++ {
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
				commentID = 0
				recoveredID = 0
				g.setCommentID(pg, 0)
				continue
			}
			return fmt.Errorf("github API returned status: %d", resp.StatusCode)
		}

		if commentID == 0 {
			var result struct {
				ID   int64 `json:"id"`
				User struct {
					ID    int64  `json:"id"`
					Login string `json:"login"`
				} `json:"user"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
				if result.ID != 0 {
					g.setCommentID(pg, result.ID)
				}
				g.authorCache.record(result.User.ID, result.User.Login)
			}
		} else if recoveredID != 0 {
			g.setCommentID(pg, recoveredID)
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
