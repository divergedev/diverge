package routing

import (
	"context"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// GatewayRouter implements Router using Gateway API HTTPRoute resources.
// This is a stub awaiting full implementation.
type GatewayRouter struct {
	Client client.Client
}

var _ Router = (*GatewayRouter)(nil)

// Reconcile is a stub that will create Gateway API HTTPRoute resources
// with header-match routing. Not yet implemented.
func (r *GatewayRouter) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	// Implement Router interface for K8s Gateway API HTTPRoute.
	// Use gateway.networking.k8s.io/v1 types.
	// Header-match filter routing.
	return nil
}

// Teardown is a stub that will clean up HTTPRoute resources.
// Not yet implemented.
func (r *GatewayRouter) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	// Cleanup HTTPRoute and related resources
	return nil
}

// GetExternalURL is a stub that will return the environment's external URL.
// Not yet implemented; returns an empty string.
func (r *GatewayRouter) GetExternalURL(env *v1alpha1.Environment) string {
	return ""
}
