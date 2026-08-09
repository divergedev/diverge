// Package routing manages Kubernetes networking resources for environment
// traffic routing, with implementations for Gateway API and Istio.
package routing

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// Router reconciles and tears down Kubernetes networking resources for a
// preview environment and provides its external URL.
type Router interface {
	Reconcile(ctx context.Context, env *v1alpha1.Environment) error
	Teardown(ctx context.Context, env *v1alpha1.Environment) error
	GetExternalURL(env *v1alpha1.Environment) string
}
