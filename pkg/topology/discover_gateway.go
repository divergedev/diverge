package topology

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// GatewayAPIDiscoverer builds a ServiceGraph from HTTPRoute and GRPCRoute
// resources in the cluster.
type GatewayAPIDiscoverer struct {
	Client client.Client
}

// Discover lists Gateway, HTTPRoute, and GRPCRoute resources to build a service
// topology graph. Only routes with accepted status are included to avoid
// producing edges for rejected or misconfigured routes.
func (d *GatewayAPIDiscoverer) Discover(ctx context.Context, namespaces []string) (*ServiceGraph, error) {
	graph := NewServiceGraph()

	var nsList []string
	if len(namespaces) > 0 {
		nsList = namespaces
	} else {
		nsList = []string{""} // Empty string means cluster-wide
	}

	for _, ns := range nsList {
		var opts []client.ListOption
		if ns != "" {
			opts = append(opts, client.InNamespace(ns))
		}

		// 1. Gateways -> Entrypoints
		var gateways gatewayv1.GatewayList
		if err := d.Client.List(ctx, &gateways, opts...); err != nil {
			return nil, fmt.Errorf("listing Gateways in namespace %q: %w", ns, err)
		}
		for _, gw := range gateways.Items {
			graph.AddEntrypoint(gw.Name)
		}

		// 2. HTTPRoutes -> Edges
		var httpRoutes gatewayv1.HTTPRouteList
		if err := d.Client.List(ctx, &httpRoutes, opts...); err != nil {
			return nil, fmt.Errorf("listing HTTPRoutes in namespace %q: %w", ns, err)
		}
		for i := range httpRoutes.Items {
			route := &httpRoutes.Items[i]
			if !isRouteAccepted(route.Status.RouteStatus) {
				continue
			}
			for _, parent := range route.Spec.ParentRefs {
				// Resolve parent gateway name, scoped to the route's namespace
				// unless an explicit namespace is specified on the parentRef.
				fromName := string(parent.Name)
				if parent.Namespace != nil && string(*parent.Namespace) != route.Namespace {
					fromName = string(*parent.Namespace) + "/" + fromName
				}

				for _, rule := range route.Spec.Rules {
					for _, backend := range rule.BackendRefs {
						toName := string(backend.Name)
						// Scope cross-namespace backend refs
						if backend.Namespace != nil && string(*backend.Namespace) != route.Namespace {
							toName = string(*backend.Namespace) + "/" + toName
						}
						graph.AddEdge(Edge{
							From:     fromName,
							To:       toName,
							Protocol: "http",
							Source:   d.Name(),
						})
					}
				}
			}
		}

		// 3. GRPCRoutes -> Edges
		var grpcRoutes gatewayv1.GRPCRouteList
		if err := d.Client.List(ctx, &grpcRoutes, opts...); err != nil {
			// GRPCRoute CRD may not be installed; skip gracefully for that case only
			if meta.IsNoMatchError(err) {
				continue
			}
			return nil, fmt.Errorf("listing GRPCRoutes in namespace %q: %w", ns, err)
		}
		for i := range grpcRoutes.Items {
			route := &grpcRoutes.Items[i]
			if !isRouteAccepted(route.Status.RouteStatus) {
				continue
			}
			for _, parent := range route.Spec.ParentRefs {
				fromName := string(parent.Name)
				if parent.Namespace != nil && string(*parent.Namespace) != route.Namespace {
					fromName = string(*parent.Namespace) + "/" + fromName
				}

				for _, rule := range route.Spec.Rules {
					for _, backend := range rule.BackendRefs {
						toName := string(backend.Name)
						if backend.Namespace != nil && string(*backend.Namespace) != route.Namespace {
							toName = string(*backend.Namespace) + "/" + toName
						}
						graph.AddEdge(Edge{
							From:     fromName,
							To:       toName,
							Protocol: "grpc",
							Source:   d.Name(),
						})
					}
				}
			}
		}
	}

	return graph, nil
}

// Name returns the discoverer source name.
func (d *GatewayAPIDiscoverer) Name() string { return "gateway-api" }

// isRouteAccepted returns true if at least one parent has accepted the route.
func isRouteAccepted(status gatewayv1.RouteStatus) bool {
	for _, parent := range status.Parents {
		for _, cond := range parent.Conditions {
			if cond.Type == string(gatewayv1.RouteConditionAccepted) && cond.Status == "True" {
				return true
			}
		}
	}
	// If no status is set (e.g. controller hasn't reconciled yet), include the route
	// to avoid false negatives during cold-start.
	return len(status.Parents) == 0
}
