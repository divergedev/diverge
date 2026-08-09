package notifier

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type Notifier interface {
	PostEnvironmentReady(ctx context.Context, env *v1alpha1.Environment) error
	PostEnvironmentFailed(ctx context.Context, env *v1alpha1.Environment, reason string) error
	PostEnvironmentTeardown(ctx context.Context, env *v1alpha1.Environment) error
}

type GitLabNotifier struct {
	Token string
}

func (g *GitLabNotifier) PostEnvironmentReady(ctx context.Context, env *v1alpha1.Environment) error {
	// Post or update MR comment
	return nil
}

func (g *GitLabNotifier) PostEnvironmentFailed(ctx context.Context, env *v1alpha1.Environment, reason string) error {
	return nil
}

func (g *GitLabNotifier) PostEnvironmentTeardown(ctx context.Context, env *v1alpha1.Environment) error {
	// Delete MR comment
	return nil
}
