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

// PreviewGroupNotifier posts deployment lifecycle events for PreviewGroups
// to an external code review platform.
type PreviewGroupNotifier interface {
	PostGroupCreated(ctx context.Context, pg *v1alpha1.PreviewGroup) error
	PostGroupReady(ctx context.Context, pg *v1alpha1.PreviewGroup) error
	PostGroupFailed(ctx context.Context, pg *v1alpha1.PreviewGroup, reason string) error
	PostGroupTeardown(ctx context.Context, pg *v1alpha1.PreviewGroup) error
	UpdateGroupStatus(ctx context.Context, pg *v1alpha1.PreviewGroup) error
}

// NoopPreviewGroupNotifier is a dummy implementation.
type NoopPreviewGroupNotifier struct{}

func (n *NoopPreviewGroupNotifier) PostGroupCreated(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	return nil
}

func (n *NoopPreviewGroupNotifier) PostGroupReady(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	return nil
}

func (n *NoopPreviewGroupNotifier) PostGroupFailed(ctx context.Context, pg *v1alpha1.PreviewGroup, reason string) error {
	return nil
}

func (n *NoopPreviewGroupNotifier) PostGroupTeardown(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	return nil
}

func (n *NoopPreviewGroupNotifier) UpdateGroupStatus(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	return nil
}

// GitLabPreviewGroupNotifier implements PreviewGroupNotifier for GitLab.
type GitLabPreviewGroupNotifier struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

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
		return "", 0, fmt.Errorf("environment is not from gitlab")
	}
	return url.PathEscape(pg.Spec.Source.Project), pg.Spec.Source.MR, nil
}

func (g *GitLabPreviewGroupNotifier) getCommentID(pg *v1alpha1.PreviewGroup) int {
	return pg.Status.CommentID
}

func (g *GitLabPreviewGroupNotifier) setCommentID(pg *v1alpha1.PreviewGroup, id int) {
	pg.Status.CommentID = id
}

func (g *GitLabPreviewGroupNotifier) postOrUpdateComment(ctx context.Context, pg *v1alpha1.PreviewGroup, body string) error {
	project, mr, err := g.getProjectAndMR(pg)
	if err != nil {
		return err
	}

	commentID := g.getCommentID(pg)
	payload := map[string]string{"body": body}
	jsonPayload, _ := json.Marshal(payload)

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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gitlab API returned status: %d", resp.StatusCode)
	}

	if commentID == 0 {
		var result struct {
			ID int `json:"id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.ID != 0 {
			g.setCommentID(pg, result.ID)
		}
	}
	return nil
}

func buildPreviewGroupTemplateData(pg *v1alpha1.PreviewGroup, reason string) PreviewGroupTemplateData {
	ttlStr := "never"
	if pg.Spec.Lifecycle != nil && pg.Spec.Lifecycle.TTL != nil {
		ttlStr = pg.Spec.Lifecycle.TTL.Duration.String()
	}

	expiryStr := "never"
	if pg.Status.ExpiresAt != nil {
		expiryStr = pg.Status.ExpiresAt.Format(time.RFC3339)
	}

	headerKey := "x-preview-env"
	if pg.Spec.Routing.HeaderKey != "" {
		headerKey = sanitizeHeaderKey(pg.Spec.Routing.HeaderKey)
	}

	headerValue := pg.Spec.Routing.HeaderValue

	var services []PreviewGroupServiceTemplateData
	runningCount := 0

	for _, specSvc := range pg.Spec.Services {
		var statusSvc v1alpha1.PreviewGroupServiceStatus
		for _, s := range pg.Status.Services {
			if s.Name == specSvc.Name {
				statusSvc = s
				break
			}
		}

		modeEmoji := "📦"
		switch specSvc.Mode {
		case v1alpha1.ServiceModeLocal:
			modeEmoji = "💻"
		case v1alpha1.ServiceModeBaseline:
			modeEmoji = "☁️"
		}

		imageOrBaseline := specSvc.Image
		switch specSvc.Mode {
		case v1alpha1.ServiceModeBaseline:
			imageOrBaseline = "N/A (baseline)"
		case v1alpha1.ServiceModeLocal:
			imageOrBaseline = specSvc.Endpoint
		}

		emoji := "⏳"
		switch statusSvc.Phase {
		case v1alpha1.PhaseRunning:
			emoji = "✅"
			runningCount++
		case v1alpha1.PhaseFailed:
			emoji = "❌"
		}

		services = append(services, PreviewGroupServiceTemplateData{
			Name:            specSvc.Name,
			Mode:            string(specSvc.Mode),
			ModeEmoji:       modeEmoji,
			ImageOrBaseline: imageOrBaseline,
			Emoji:           emoji,
			Phase:           string(statusSvc.Phase),
			Namespace:       statusSvc.Namespace,
			Reason:          statusSvc.Reason,
		})
	}

	baseURL := ""
	if pg.Spec.Routing.ExternalURL != "" {
		baseURL = pg.Spec.Routing.ExternalURL
	}

	urlStr := pg.Spec.Routing.ExternalURL
	if urlStr == "" {
		urlStr = "https://preview.example.com" // Placeholder since it's not defined
	}

	return PreviewGroupTemplateData{
		Name:         pg.Name,
		Branch:       pg.Spec.Source.Branch,
		MR:           pg.Spec.Source.MR,
		TTL:          ttlStr,
		URL:          urlStr,
		BaseURL:      baseURL,
		HeaderKey:    headerKey,
		HeaderValue:  headerValue,
		ServiceCount: len(pg.Spec.Services),
		RunningCount: runningCount,
		ExpiryTime:   expiryStr,
		Reason:       reason,
		Services:     services,
	}
}

func (g *GitLabPreviewGroupNotifier) PostGroupCreated(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	data := buildPreviewGroupTemplateData(pg, "")
	msg, err := renderTemplate(pgCreatedTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}

func (g *GitLabPreviewGroupNotifier) PostGroupReady(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	data := buildPreviewGroupTemplateData(pg, "")
	msg, err := renderTemplate(pgReadyTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}

func (g *GitLabPreviewGroupNotifier) PostGroupFailed(ctx context.Context, pg *v1alpha1.PreviewGroup, reason string) error {
	data := buildPreviewGroupTemplateData(pg, reason)
	msg, err := renderTemplate(pgFailedTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}

func (g *GitLabPreviewGroupNotifier) PostGroupTeardown(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	data := buildPreviewGroupTemplateData(pg, "Teardown requested")
	msg, err := renderTemplate(pgTeardownTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}

func (g *GitLabPreviewGroupNotifier) UpdateGroupStatus(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	data := buildPreviewGroupTemplateData(pg, "")
	msg, err := renderTemplate(pgReadyTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, pg, msg)
}
