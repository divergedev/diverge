package routing

import (
	"context"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type IstioRouter struct {
	Client client.Client
}

var _ Router = (*IstioRouter)(nil)

func (r *IstioRouter) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	// Generate Istio VirtualService with header-match routing.
	// Include logic to strip external x-diverge-env headers at ingress.
	return nil
}

func (r *IstioRouter) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	// Cleanup VirtualService and related resources
	return nil
}

func (r *IstioRouter) GetExternalURL(env *v1alpha1.Environment) string {
	return ""
}
