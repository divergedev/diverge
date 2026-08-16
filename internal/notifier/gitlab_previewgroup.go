package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// GitLabPreviewGroupNotifier implements PreviewGroupNotifier for GitLab.
type GitLabPreviewGroupNotifier struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewGitLabPreviewGroupNotifier performs its designated operation.
func NewGitLabPreviewGroupNotifier(baseURL, token string) *GitLabPreviewGroupNotifier {
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

func (g *GitLabPreviewGroupNotifier) postOrUpdateComment(ctx context.Context, pg *v1alpha1.PreviewGroup, body string) error {
	project, mr, err := g.getProjectAndMR(pg)
	if err != nil {
		return err
	}

	payload := map[string]string{"body": body}
	jsonPayload, _ := json.Marshal(payload)

	for attempt := 0; attempt < 3; attempt++ {
		commentID := g.getCommentID(pg)
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
				g.setCommentID(pg, 0)
				continue
			}
			return fmt.Errorf("gitlab API returned status: %d", resp.StatusCode)
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
