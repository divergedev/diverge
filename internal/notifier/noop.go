package notifier

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type NoopNotifier struct{}

func (n *NoopNotifier) PostEnvironmentCreated(ctx context.Context, env *v1alpha1.Environment) error {
	return nil
}

func (n *NoopNotifier) PostEnvironmentReady(ctx context.Context, env *v1alpha1.Environment) error {
	return nil
}

func (n *NoopNotifier) PostEnvironmentFailed(ctx context.Context, env *v1alpha1.Environment, reason string) error {
	return nil
}

func (n *NoopNotifier) PostEnvironmentTeardown(ctx context.Context, env *v1alpha1.Environment) error {
	return nil
}

func (n *NoopNotifier) UpdateEnvironmentStatus(ctx context.Context, env *v1alpha1.Environment) error {
	return nil
}
