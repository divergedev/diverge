// Package testing provides test integration for preview environments.
// It triggers CI pipeline runs and polls for their completion status.
package testing

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// TestResult holds the result of a test run poll.
type TestResult struct {
	State   v1alpha1.TestState
	Summary string
	URL     string
}

// TestRunner triggers test runs and polls for their completion.
type TestRunner interface {
	// Trigger starts a test run against the preview environment.
	// Returns an opaque RunID that can be used for subsequent Status polls.
	Trigger(ctx context.Context, env *v1alpha1.Environment) (runID string, err error)

	// Status polls for the current state of a previously triggered test run.
	// Returns a TestResult with State=TestStateRunning while the run is in progress.
	Status(ctx context.Context, env *v1alpha1.Environment, runID string) (*TestResult, error)
}
