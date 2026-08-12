package routing

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// GatewayRouter implements Router using Gateway API HTTPRoute resources.
// It creates HTTPRoute resources with header-based routing to direct
// traffic to preview environment services, mirroring the GRPCRouter
// pattern for HTTP traffic.
type GatewayRouter struct {
	Client    client.Client
	Namespace string
}

var _ Router = (*GatewayRouter)(nil)

// Reconcile creates or updates HTTPRoute resources for each changed service
// in the environment, configuring header-based routing rules.
func (r *GatewayRouter) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	logger := log.FromContext(ctx).WithName("gateway-router")

	headerKey := env.Spec.Routing.HeaderKey
	if headerKey == "" {
		headerKey = "x-diverge-env"
	}

	headerValue := env.Spec.Routing.HeaderValue
	if headerValue == "" {
		headerValue = env.Name
	}

	parentRefName := "diverge-gateway"
	backendPort := int64(8080)
	if cfg := env.Spec.ServiceConfig; cfg != nil {
		if cfg.ParentRef != "" {
			parentRefName = cfg.ParentRef
		}
		if cfg.Port > 0 {
			backendPort = int64(cfg.Port)
		}
		if cfg.HeaderKey != "" {
			headerKey = cfg.HeaderKey
		}
	}

	ns := r.namespace(env)

	for _, svc := range env.Spec.Deploy.ChangedServices {
		routeName := fmt.Sprintf("%s-%s", env.Name, svc)

		u := &unstructured.Unstructured{}
		u.SetAPIVersion("gateway.networking.k8s.io/v1")
		u.SetKind("HTTPRoute")
		u.SetName(routeName)
		u.SetNamespace(ns)
		u.SetLabels(map[string]string{
			"diverge.io/environment": env.Name,
			"diverge.io/managed-by":  "diverge",
		})

		// Build the match criteria: header + optional path prefix
		matchRule := map[string]interface{}{
			"headers": []interface{}{
				map[string]interface{}{
					"type":  "Exact",
					"name":  headerKey,
					"value": headerValue,
				},
			},
		}
		// Scope the route to the service's path prefix (e.g., /api/payments)
		// Without this, the preview route matches all paths and shadows baseline
		if cfg := env.Spec.ServiceConfig; cfg != nil && cfg.PathPrefix != "" {
			matchRule["path"] = map[string]interface{}{
				"type":  "PathPrefix",
				"value": cfg.PathPrefix,
			}
		}

		spec := map[string]interface{}{
			"parentRefs": []interface{}{
				map[string]interface{}{
					"name": parentRefName,
				},
			},
			"rules": []interface{}{
				map[string]interface{}{
					"matches": []interface{}{
						matchRule,
					},
					"backendRefs": []interface{}{
						map[string]interface{}{
							"name": fmt.Sprintf("%s-%s", env.Name, svc),
							"port": backendPort,
						},
					},
				},
			},
		}

		existing := &unstructured.Unstructured{}
		existing.SetAPIVersion("gateway.networking.k8s.io/v1")
		existing.SetKind("HTTPRoute")

		err := r.Client.Get(ctx, client.ObjectKey{Name: routeName, Namespace: ns}, existing)
		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to get HTTPRoute for %s: %w", svc, err)
			}
			u.Object["spec"] = spec
			if err := r.Client.Create(ctx, u); err != nil {
				return fmt.Errorf("failed to create HTTPRoute for %s: %w", svc, err)
			}
			logger.V(1).Info("Created HTTPRoute", "name", routeName, "service", svc)
		} else {
			existing.Object["spec"] = spec
			if err := r.Client.Update(ctx, existing); err != nil {
				return fmt.Errorf("failed to update HTTPRoute for %s: %w", svc, err)
			}
			logger.V(1).Info("Updated HTTPRoute", "name", routeName, "service", svc)
		}
	}

	logger.Info("Reconciled HTTPRoutes",
		"environment", env.Name,
		"services", len(env.Spec.Deploy.ChangedServices),
	)
	return nil
}

// Teardown deletes all HTTPRoute resources associated with the environment
// by selecting on the diverge.io/environment label. This ensures stale routes
// from removed services are cleaned up.
func (r *GatewayRouter) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	logger := log.FromContext(ctx).WithName("gateway-router")
	ns := r.namespace(env)

	selector := labels.SelectorFromSet(map[string]string{
		"diverge.io/environment": env.Name,
		"diverge.io/managed-by":  "diverge",
	})

	var routeList unstructured.UnstructuredList
	routeList.SetAPIVersion("gateway.networking.k8s.io/v1")
	routeList.SetKind("HTTPRouteList")

	if err := r.Client.List(ctx, &routeList,
		client.InNamespace(ns),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		return fmt.Errorf("failed to list HTTPRoutes for environment %s: %w", env.Name, err)
	}

	for i := range routeList.Items {
		if err := r.Client.Delete(ctx, &routeList.Items[i]); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete HTTPRoute %s: %w", routeList.Items[i].GetName(), err)
			}
		}
	}

	logger.Info("Tore down HTTPRoutes",
		"environment", env.Name,
		"deleted", len(routeList.Items),
	)
	return nil
}

// GetExternalURL returns the environment's external URL by substituting
// {env} in the routing template, or an empty string if none is configured.
func (r *GatewayRouter) GetExternalURL(env *v1alpha1.Environment) string {
	if env.Spec.Routing.ExternalURL != "" {
		return strings.ReplaceAll(env.Spec.Routing.ExternalURL, "{env}", env.Name)
	}
	return ""
}

// namespace returns the target namespace for routing resources.
func (r *GatewayRouter) namespace(env *v1alpha1.Environment) string {
	if r.Namespace != "" {
		return r.Namespace
	}
	return env.Namespace
}
