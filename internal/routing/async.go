package routing

import (
	"context"
	"errors"
	"fmt"

	"github.com/divergedev/diverge/api/v1alpha1"
)

var _ Router = (*AsyncRouter)(nil)

// AsyncProvider routes asynchronous messages through preview environments.
type AsyncProvider interface {
	Name() string
	Reconcile(ctx context.Context, env *v1alpha1.Environment) error
	Teardown(ctx context.Context, env *v1alpha1.Environment) error
}

// AsyncRouter delegates to registered async providers.
type AsyncRouter struct {
	Providers []AsyncProvider
}

// Reconcile performs its designated operation.
func (r *AsyncRouter) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	var errs []error
	for _, p := range r.Providers {
		if err := p.Reconcile(ctx, env); err != nil {
			errs = append(errs, fmt.Errorf("async provider %s: %w", p.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// Teardown performs its designated operation.
func (r *AsyncRouter) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	// Reverse order, collect all errors
	var errs []error
	for i := len(r.Providers) - 1; i >= 0; i-- {
		if err := r.Providers[i].Teardown(ctx, env); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// GetExternalURL performs its designated operation.
func (r *AsyncRouter) GetExternalURL(env *v1alpha1.Environment) string {
	return "" // Async has no URL
}
