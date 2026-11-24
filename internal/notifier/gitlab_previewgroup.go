package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// GitLabPreviewGroupNotifier implements PreviewGroupNotifier for GitLab.
type GitLabPreviewGroupNotifier struct {
	BaseURL        string
	Token          string
	HTTPClient     *http.Client
	AuthorUsername string

	authorCache cachedNotifierAuthor
}

// NewGitLabPreviewGroupNotifier performs its designated operation.
func NewGitLabPreviewGroupNotifier(baseURL, token string) *GitLabPreviewGroupNotifier {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://gitlab.com"
	}
	return &GitLabPreviewGroupNotifier{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (g *GitLabPreviewGroupNotifier) getProjectAndMR(pg *v1alpha1.PreviewGroup) (string, int, error) {
	if pg.Spec.Source.Provider != "gitlab" {
		return "", 0, ErrNotGitLabSource
	}
	if pg.Spec.Source.MR == 0 {
		return "", 0, ErrMissingMergeRequest
	}
	return url.PathEscape(pg.Spec.Source.Project), pg.Spec.Source.MR, nil
}

func (g *GitLabPreviewGroupNotifier) getCommentID(pg *v1alpha1.PreviewGroup) int64 {
	return pg.Status.CommentID
}

func (g *GitLabPreviewGroupNotifier) setCommentID(pg *v1alpha1.PreviewGroup, id int64) {
	pg.Status.CommentID = id
}

func (g *GitLabPreviewGroupNotifier) resolveAuthenticatedAuthor(ctx context.Context) (int64, string, error) {
	if id, username, needFetch := g.authorCache.get(g.AuthorUsername); !needFetch {
		return id, username, nil
	}
	baseURL := strings.TrimRight(strings.TrimSpace(g.BaseURL), "/")
	if baseURL == "" {
		return 0, "", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v4/user", nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("PRIVATE-TOKEN", g.Token)
	httpClient := g.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("gitlab /api/v4/user lookup failed: %w", err)
	}
	if resp.StatusCode == http.StatusForbidden &&
		(resp.Header.Get("Retry-After") != "" || resp.Header.Get("RateLimit-Remaining") == "0") {
		_ = resp.Body.Close()
		return 0, "", fmt.Errorf("gitlab /api/v4/user lookup rate limited: status %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		g.authorCache.markChecked()
		return 0, "", nil
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return 0, "", fmt.Errorf("gitlab /api/v4/user lookup returned status: %d", resp.StatusCode)
	}
	var u struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	decErr := json.NewDecoder(resp.Body).Decode(&u)
	_ = resp.Body.Close()
	if decErr == nil && strings.TrimSpace(u.Username) != "" {
		username := strings.TrimSpace(u.Username)
		g.authorCache.record(u.ID, username)
		return u.ID, username, nil
	}
	g.authorCache.markChecked()
	return 0, "", nil
}

func (g *GitLabPreviewGroupNotifier) findExistingNoteID(ctx context.Context, escapedProject string, mr int, marker string) (int64, error) {
	if marker == "" {
		return 0, nil
	}
	authID, authUsername, err := g.resolveAuthenticatedAuthor(ctx)
	if err != nil {
		return 0, err
	}
	if authID == 0 && authUsername == "" {
		return 0, nil
	}
	httpClient := g.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	nextURL := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d/notes?per_page=100&sort=desc&order_by=updated_at", g.BaseURL, escapedProject, mr)
	for page := 0; page < 5 && nextURL != ""; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("PRIVATE-TOKEN", g.Token)
		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, fmt.Errorf("gitlab note lookup failed: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return 0, fmt.Errorf("gitlab note lookup returned status: %d", resp.StatusCode)
		}
		var notes []struct {
			ID     int64  `json:"id"`
			Body   string `json:"body"`
			Author struct {
				ID       int64  `json:"id"`
				Username string `json:"username"`
			} `json:"author"`
		}
		decErr := json.NewDecoder(resp.Body).Decode(&notes)
		linkHeader := resp.Header.Get("Link")
		_ = resp.Body.Close()
		if decErr != nil {
			return 0, fmt.Errorf("decoding gitlab note lookup response: %w", decErr)
		}
		for _, n := range notes {
			if n.ID != 0 && notifierAuthorMatches(authID, authUsername, n.Author.ID, n.Author.Username) && strings.Contains(n.Body, marker) {
				return n.ID, nil
			}
		}
		nextURL = parseNextLink(linkHeader)
	}
	return 0, nil
}

func (g *GitLabPreviewGroupNotifier) postOrUpdateComment(ctx context.Context, pg *v1alpha1.PreviewGroup, body string) error {
	project, mr, err := g.getProjectAndMR(pg)
	if err != nil {
		return err
	}

	commentID := g.getCommentID(pg)
	var recoveredID int64
	if commentID == 0 {
		existingID, lookupErr := g.findExistingNoteID(ctx, project, mr, previewGroupCommentMarker(pg))
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

	for attempt := 0; attempt < 3; attempt++ {
		var reqURL string
		var method string

		if commentID != 0 {
			reqURL = fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d/notes/%d", g.BaseURL, project, mr, commentID)
			method = http.MethodPut
		} else {
			reqURL = fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d/notes", g.BaseURL, project, mr)
			method = http.MethodPost
		}

		req, err := http.NewRequestWithContext(ctx, method, reqURL, bytes.NewReader(jsonPayload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("PRIVATE-TOKEN", g.Token)

		resp, err := g.HTTPClient.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
			retryAfter := resp.Header.Get("Retry-After")
			backoff := 5 * time.Second
			if secs, err := strconv.Atoi(retryAfter); err == nil {
				backoff = time.Duration(secs) * time.Second
			}
			_ = resp.Body.Close()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			continue // retry
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_ = resp.Body.Close()
			if commentID != 0 && resp.StatusCode == http.StatusNotFound {
				commentID = 0
				recoveredID = 0
				g.setCommentID(pg, 0)
				continue
			}
			return fmt.Errorf("gitlab API returned status: %d", resp.StatusCode)
		}

		if commentID == 0 {
			var result struct {
				ID     int64 `json:"id"`
				Author struct {
					ID       int64  `json:"id"`
					Username string `json:"username"`
				} `json:"author"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
				if result.ID != 0 {
					g.setCommentID(pg, result.ID)
				}
				g.authorCache.record(result.Author.ID, result.Author.Username)
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
func (g *GitLabPreviewGroupNotifier) PostGroupCreated(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	data := buildPreviewGroupTemplateData(pg, "")
	msg, err := renderTemplate(pgCreatedTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}

// PostGroupReady performs its designated operation.
func (g *GitLabPreviewGroupNotifier) PostGroupReady(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	data := buildPreviewGroupTemplateData(pg, "")
	msg, err := renderTemplate(pgReadyTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}

// PostGroupFailed performs its designated operation.
func (g *GitLabPreviewGroupNotifier) PostGroupFailed(ctx context.Context, pg *v1alpha1.PreviewGroup, reason string) error {
	data := buildPreviewGroupTemplateData(pg, reason)
	msg, err := renderTemplate(pgFailedTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}

// PostGroupTeardown performs its designated operation.
func (g *GitLabPreviewGroupNotifier) PostGroupTeardown(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	data := buildPreviewGroupTemplateData(pg, "Teardown requested")
	msg, err := renderTemplate(pgTeardownTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}

// UpdateGroupStatus performs its designated operation.
func (g *GitLabPreviewGroupNotifier) UpdateGroupStatus(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	data := buildPreviewGroupTemplateData(pg, "")
	msg, err := renderTemplate(pgReadyTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}
