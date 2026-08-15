package notifier

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// NoopStatusReporter represents the configuration or state for this type.
type NoopStatusReporter struct{}

// PostCommitStatus performs its designated operation.
func (n *NoopStatusReporter) PostCommitStatus(ctx context.Context, env *v1alpha1.Environment, state, description string) error {
	return nil
}
