package routing

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/divergedev/diverge/api/v1alpha1"
	"golang.org/x/exp/maps"
)

var _ Router = (*CompositeRouter)(nil)

// PartialFailureError indicates some routers succeeded but others failed.
type PartialFailureError struct {
	Succeeded []string
	Failed    map[string]error
}

func (e *PartialFailureError) Error() string {
	var msgs []string
	for name, err := range e.Failed {
		msgs = append(msgs, fmt.Sprintf("%s: %v", name, err))
	}
	return fmt.Sprintf("partial failure (succeeded: %v, failed: %v)", e.Succeeded, strings.Join(msgs, ", "))
}

// CompositeRouter runs multiple routers.
type CompositeRouter struct {
	Routers map[string]Router // named routers: "sync", "async", etc.
}

func (r *CompositeRouter) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	var succeeded []string
	failed := make(map[string]error)
	for name, router := range r.Routers {
		if err := router.Reconcile(ctx, env); err != nil {
			failed[name] = err
		} else {
			succeeded = append(succeeded, name)
		}
	}
	if len(failed) > 0 && len(succeeded) > 0 {
		return &PartialFailureError{Succeeded: succeeded, Failed: failed}
	}
	// All failed
	if len(failed) > 0 {
		return errors.Join(maps.Values(failed)...)
	}
	return nil
}

func (r *CompositeRouter) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	var errs []error
	for _, router := range r.Routers {
		if err := router.Teardown(ctx, env); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *CompositeRouter) GetExternalURL(env *v1alpha1.Environment) string {
	for _, router := range r.Routers {
		if url := router.GetExternalURL(env); url != "" {
			return url
		}
	}
	return ""
}
