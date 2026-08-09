package routing

import (
	"context"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// IstioRouter implements Router using Istio VirtualService resources.
// This is a stub awaiting full implementation.
type IstioRouter struct {
	Client client.Client
}

var _ Router = (*IstioRouter)(nil)

// Reconcile is a stub that will generate Istio VirtualService resources
// with header-match routing. Not yet implemented.
func (r *IstioRouter) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	// Generate Istio VirtualService with header-match routing.
	// Include logic to strip external x-diverge-env headers at ingress.
	return nil
}

// Teardown is a stub that will clean up VirtualService resources.
// Not yet implemented.
func (r *IstioRouter) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	// Cleanup VirtualService and related resources
	return nil
}

// GetExternalURL is a stub that will return the environment's external URL.
// Not yet implemented; returns an empty string.
func (r *IstioRouter) GetExternalURL(env *v1alpha1.Environment) string {
	return ""
}
