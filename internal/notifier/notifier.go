package notifier

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type Notifier interface {
	PostEnvironmentCreated(ctx context.Context, env *v1alpha1.Environment) error
	PostEnvironmentReady(ctx context.Context, env *v1alpha1.Environment) error
	PostEnvironmentFailed(ctx context.Context, env *v1alpha1.Environment, reason string) error
	PostEnvironmentTeardown(ctx context.Context, env *v1alpha1.Environment) error
	UpdateEnvironmentStatus(ctx context.Context, env *v1alpha1.Environment) error
}
