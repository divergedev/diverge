package notifier

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// StatusReporter posts commit status checks to SCM platforms for merge gating.
type StatusReporter interface {
	PostCommitStatus(ctx context.Context, env *v1alpha1.Environment, state, description string) error
}
