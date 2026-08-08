package routing

import (
	"context"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type IstioRouter struct {
	Client client.Client
}

func (r *IstioRouter) GenerateRules(ctx context.Context, namespace string, changedServices []string, headerKey, headerValue string) error {
	// Generate VirtualService for header-based routing
	// Generate DestinationRule for service subsets
	return nil
}

func (r *IstioRouter) Cleanup(ctx context.Context, namespace string, headerValue string) error {
	// Remove specific subset rules
	return nil
}
