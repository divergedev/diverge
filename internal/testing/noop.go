package testing

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// NoopTestRunner is a no-op implementation used when testing is not configured.
type NoopTestRunner struct{}

var _ TestRunner = (*NoopTestRunner)(nil)

func (n *NoopTestRunner) Trigger(_ context.Context, _ *v1alpha1.Environment) (string, error) {
	return "", nil
}

func (n *NoopTestRunner) Status(_ context.Context, _ *v1alpha1.Environment, _ string) (*TestResult, error) {
	return &TestResult{State: v1alpha1.TestStatePassed, Summary: "no tests configured"}, nil
}
