package routing

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

	ns := r.namespace(env)

	for _, svc := range env.Spec.Deploy.ChangedServices {
		routeName := fmt.Sprintf("%s-%s", env.Name, svc)

		u := &unstructured.Unstructured{}
		u.SetAPIVersion("gateway.networking.k8s.io/v1")
		u.SetKind("HTTPRoute")
		u.SetName(routeName)
		u.SetNamespace(ns)

		spec := map[string]interface{}{
			"parentRefs": []interface{}{
				map[string]interface{}{
					"name": "diverge-gateway",
				},
			},
			"rules": []interface{}{
				map[string]interface{}{
					"matches": []interface{}{
						map[string]interface{}{
							"headers": []interface{}{
								map[string]interface{}{
									"type":  "Exact",
									"name":  headerKey,
									"value": headerValue,
								},
							},
						},
					},
					"backendRefs": []interface{}{
						map[string]interface{}{
							"name": fmt.Sprintf("%s-%s", env.Name, svc),
							"port": int64(8080),
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

// Teardown deletes all HTTPRoute resources associated with the environment.
func (r *GatewayRouter) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	logger := log.FromContext(ctx).WithName("gateway-router")
	ns := r.namespace(env)

	for _, svc := range env.Spec.Deploy.ChangedServices {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion("gateway.networking.k8s.io/v1")
		u.SetKind("HTTPRoute")
		u.SetName(fmt.Sprintf("%s-%s", env.Name, svc))
		u.SetNamespace(ns)

		if err := r.Client.Delete(ctx, u); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete HTTPRoute for %s: %w", svc, err)
			}
		}
	}

	logger.Info("Tore down HTTPRoutes",
		"environment", env.Name,
		"services", len(env.Spec.Deploy.ChangedServices),
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
