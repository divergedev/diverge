package notifier

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// NoopStatusReporter suppresses commit status reporting; PostCommitStatus returns nil.
type NoopStatusReporter struct{}

// PostCommitStatus intentionally performs no notification; returns nil.
func (n *NoopStatusReporter) PostCommitStatus(ctx context.Context, env *v1alpha1.Environment, state, description string) error {
	return nil
}
