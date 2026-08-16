package notifier

import (
	"context"
	"errors"
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

	baseURL := pg.Spec.Routing.ExternalURL

	return PreviewGroupTemplateData{
		Name:         pg.Name,
		Branch:       pg.Spec.Source.Branch,
		MR:           pg.Spec.Source.MR,
		TTL:          ttlStr,
		URL:          baseURL,
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
