package routing

import (
	"context"
	"github.com/divergedev/diverge/api/v1alpha1"
)

// NoopRouter is a no-op Router implementation for testing and environments
// where routing reconciliation is disabled.
type NoopRouter struct{}

var _ Router = (*NoopRouter)(nil)

// Reconcile intentionally performs no routing; returns nil.
func (r *NoopRouter) Reconcile(_ context.Context, _ *v1alpha1.Environment) error { return nil }

// Teardown performs its designated operation.
func (r *NoopRouter) Teardown(_ context.Context, _ *v1alpha1.Environment) error { return nil }

// GetExternalURL returns empty string.
func (r *NoopRouter) GetExternalURL(_ *v1alpha1.Environment) string { return "" }
