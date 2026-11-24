package notifier

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
)

var (
	// ErrNotGitLabSource indicates the project is not a GitLab project
	ErrNotGitLabSource = errors.New("preview group source is not gitlab")
	// ErrMissingMergeRequest indicates no merge request is associated
	ErrMissingMergeRequest = errors.New("preview group has no merge request number")
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

// PostGroupCreated performs its designated operation.
func (n *NoopPreviewGroupNotifier) PostGroupCreated(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	return nil
}

// PostGroupReady performs its designated operation.
func (n *NoopPreviewGroupNotifier) PostGroupReady(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	return nil
}

// PostGroupFailed performs its designated operation.
func (n *NoopPreviewGroupNotifier) PostGroupFailed(ctx context.Context, pg *v1alpha1.PreviewGroup, reason string) error {
	return nil
}

// PostGroupTeardown performs its designated operation.
func (n *NoopPreviewGroupNotifier) PostGroupTeardown(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
	return nil
}

// UpdateGroupStatus performs its designated operation.
func (n *NoopPreviewGroupNotifier) UpdateGroupStatus(ctx context.Context, pg *v1alpha1.PreviewGroup) error {
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

	cleanBaseURL, openPreviewURL, exitPreviewURL := buildPreviewGroupLinks(pg.Spec.Routing.ExternalURL, headerValue)

	return PreviewGroupTemplateData{
		Name:         pg.Name,
		Branch:       pg.Spec.Source.Branch,
		MR:           pg.Spec.Source.MR,
		TTL:          ttlStr,
		URL:          openPreviewURL,
		ExitURL:      exitPreviewURL,
		BaseURL:      cleanBaseURL,
		HeaderKey:    headerKey,
		HeaderValue:  headerValue,
		ServiceCount: len(pg.Spec.Services),
		RunningCount: runningCount,
		ExpiryTime:   expiryStr,
		Reason:       reason,
		Services:     services,
	}
}

func buildPreviewGroupLinks(rawURL, headerValue string) (cleanBase, openURL, exitURL string) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", "", ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return trimmed, trimmed, ""
	}
	baseCopy := *parsed
	baseQ := baseCopy.Query()
	baseQ.Del("_diverge_env")
	baseCopy.RawQuery = baseQ.Encode()
	cleanBase = baseCopy.String()

	openCopy := *parsed
	openQ := openCopy.Query()
	if strings.TrimSpace(headerValue) != "" {
		openQ.Set("_diverge_env", strings.TrimSpace(headerValue))
	}
	openCopy.RawQuery = openQ.Encode()
	openURL = openCopy.String()

	exitCopy := *parsed
	exitQ := exitCopy.Query()
	exitQ.Set("_diverge_env", "clear")
	exitCopy.RawQuery = exitQ.Encode()
	exitURL = exitCopy.String()
	return cleanBase, openURL, exitURL
}

func previewGroupCommentMarker(pg *v1alpha1.PreviewGroup) string {
	if pg == nil {
		return ""
	}
	src := fmt.Sprintf("%s:%s#%d",
		strings.TrimSpace(pg.Spec.Source.Provider),
		strings.TrimSpace(pg.Spec.Source.Project),
		pg.Spec.Source.MR,
	)
	return fmt.Sprintf("<!-- diverge:previewgroup=%s source=%s -->", strings.TrimSpace(pg.Name), src)
}

func withPreviewGroupCommentMarker(pg *v1alpha1.PreviewGroup, body string) string {
	marker := previewGroupCommentMarker(pg)
	if marker == "" || strings.Contains(body, marker) {
		return body
	}
	return body + "\n\n" + marker
}

func parseNextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		segments := strings.Split(strings.TrimSpace(part), ";")
		if len(segments) < 2 {
			continue
		}
		rawURL := strings.TrimSpace(segments[0])
		if !strings.HasPrefix(rawURL, "<") || !strings.HasSuffix(rawURL, ">") {
			continue
		}
		for _, param := range segments[1:] {
			p := strings.TrimSpace(param)
			if strings.EqualFold(p, `rel="next"`) || strings.EqualFold(p, "rel=next") {
				return rawURL[1 : len(rawURL)-1]
			}
		}
	}
	return ""
}
