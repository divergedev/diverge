package routing

import (
	"context"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type GatewayRouter struct {
	Client client.Client
}

var _ Router = (*GatewayRouter)(nil)

func (r *GatewayRouter) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	// Implement Router interface for K8s Gateway API HTTPRoute.
	// Use gateway.networking.k8s.io/v1 types.
	// Header-match filter routing.
	return nil
}

func (r *GatewayRouter) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	// Cleanup HTTPRoute and related resources
	return nil
}

func (r *GatewayRouter) GetExternalURL(env *v1alpha1.Environment) string {
	return ""
}
