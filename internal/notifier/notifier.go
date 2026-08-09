// Package notifier provides integrations for posting deployment status
// notifications to code review platforms such as GitHub and GitLab.
package notifier

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// Notifier posts deployment lifecycle events to an external code review
// platform. Implementations exist for GitHub, GitLab, and a no-op stub.
type Notifier interface {
	PostEnvironmentCreated(ctx context.Context, env *v1alpha1.Environment) error
	PostEnvironmentReady(ctx context.Context, env *v1alpha1.Environment) error
	PostEnvironmentFailed(ctx context.Context, env *v1alpha1.Environment, reason string) error
	PostEnvironmentTeardown(ctx context.Context, env *v1alpha1.Environment) error
	UpdateEnvironmentStatus(ctx context.Context, env *v1alpha1.Environment) error
}
