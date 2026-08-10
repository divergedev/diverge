package notifier

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type NoopStatusReporter struct{}

func (n *NoopStatusReporter) PostCommitStatus(ctx context.Context, env *v1alpha1.Environment, state, description string) error {
	return nil
}
