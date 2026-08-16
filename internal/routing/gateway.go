package routing

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
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

// ErrHostnameTooLong is returned when a derived subdomain hostname exceeds the
// DNS maximum of 253 characters.
var ErrHostnameTooLong = errors.New("derived hostname exceeds 253 characters")

// Reconcile creates or updates routing resources for each changed service
// in the environment, configuring header-based routing rules.
// Supports both HTTPRoute (default) and GRPCRoute (when protocol=grpc).
// Also creates GAMMA mesh routes (parentRef=Service) for east-west traffic.
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
	protocol := "http"
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
		if cfg.Protocol != "" {
			protocol = cfg.Protocol
		}
	}

	ns := r.namespace(env)

	for _, svc := range env.Spec.Deploy.ChangedServices {
		routeName := fmt.Sprintf("%s-%s", env.Name, svc)

		if protocol == "grpc" {
			if err := r.reconcileGRPCRoute(ctx, env, routeName, svc, ns, parentRefName, headerKey, headerValue, backendPort); err != nil {
				return err
			}
		} else {
			if err := r.reconcileHTTPRoute(ctx, env, routeName, svc, ns, parentRefName, headerKey, headerValue, backendPort); err != nil {
				return err
			}
		}

		// GAMMA mesh route: parentRef is the baseline Service for east-west routing
		if cfg := env.Spec.ServiceConfig; cfg != nil && cfg.ServiceName != "" {
			meshRouteName := fmt.Sprintf("%s-%s-mesh", env.Name, svc)
			if protocol == "grpc" {
				if err := r.reconcileGRPCRoute(ctx, env, meshRouteName, svc, ns, cfg.ServiceName, headerKey, headerValue, backendPort); err != nil {
					return err
				}
			} else {
				if err := r.reconcileHTTPRoute(ctx, env, meshRouteName, svc, ns, cfg.ServiceName, headerKey, headerValue, backendPort); err != nil {
					return err
				}
			}
		}
	}

	logger.Info("Reconciled routes",
		"environment", env.Name,
		"services", len(env.Spec.Deploy.ChangedServices),
		"protocol", protocol,
	)
	return nil
}

// reconcileRoute creates or updates a single route (HTTPRoute or GRPCRoute).
func (r *GatewayRouter) reconcileRoute(ctx context.Context, env *v1alpha1.Environment, kind, apiVersion, routeName, svc, ns, parentRefName, headerKey, headerValue string, backendPort int64) error {
	logger := log.FromContext(ctx).WithName("gateway-router")

	u := &unstructured.Unstructured{}
	u.SetAPIVersion(apiVersion)
	u.SetKind(kind)
	u.SetName(routeName)
	u.SetNamespace(ns)
	u.SetLabels(map[string]string{
		"diverge.io/environment": env.Name,
		"diverge.io/managed-by":  "diverge",
	})

	var hostnames []interface{}
	var matches []interface{}

	if env.Spec.Routing.Mode == "subdomain" && env.Spec.Routing.BaseDomain != "" {
		// Subdomain mode: route by hostname, no header match needed
		hostname := fmt.Sprintf("%s.%s", env.Name, env.Spec.Routing.BaseDomain)
		if len(hostname) > 253 {
			return fmt.Errorf("%w: %q (%d chars)", ErrHostnameTooLong, hostname, len(hostname))
		}
		hostnames = append(hostnames, hostname)
		// No header matches - all traffic to this hostname goes to preview
		matchRule := map[string]interface{}{}
		if kind == "HTTPRoute" {
			if cfg := env.Spec.ServiceConfig; cfg != nil && cfg.PathPrefix != "" {
				matchRule["path"] = map[string]interface{}{
					"type":  "PathPrefix",
					"value": cfg.PathPrefix,
				}
			}
		}
		matches = []interface{}{matchRule}
	} else {
		// Header mode (default): match on header
		matchRule := map[string]interface{}{
			"headers": []interface{}{
				map[string]interface{}{
					"type":  "Exact",
					"name":  headerKey,
					"value": headerValue,
				},
			},
		}
		if kind == "HTTPRoute" {
			if cfg := env.Spec.ServiceConfig; cfg != nil && cfg.PathPrefix != "" {
				matchRule["path"] = map[string]interface{}{
					"type":  "PathPrefix",
					"value": cfg.PathPrefix,
				}
			}
		}
		matches = []interface{}{matchRule}

		if env.Spec.Routing.Cookie != nil && env.Spec.Routing.Cookie.Enabled && kind == "HTTPRoute" {
			cookieMatch := map[string]interface{}{
				"headers": []interface{}{
					map[string]interface{}{
						"type":  "RegularExpression",
						"name":  "Cookie",
						"value": fmt.Sprintf(`(?:^|;\s*)%s=%s(?:;|$)`, regexp.QuoteMeta(headerKey), regexp.QuoteMeta(headerValue)),
					},
				},
			}
			if kind == "HTTPRoute" {
				if cfg := env.Spec.ServiceConfig; cfg != nil && cfg.PathPrefix != "" {
					cookieMatch["path"] = map[string]interface{}{
						"type":  "PathPrefix",
						"value": cfg.PathPrefix,
					}
				}
			}
			matches = append(matches, cookieMatch)
		}
	}

	parentRef := map[string]interface{}{
		"name": parentRefName,
	}
	if isServiceName(parentRefName) {
		parentRef["kind"] = "Service"
		parentRef["group"] = ""
	}

	rule := map[string]interface{}{
		"matches": matches,
		"backendRefs": []interface{}{
			map[string]interface{}{
				"name": fmt.Sprintf("%s-%s", env.Name, svc),
				"port": backendPort,
			},
		},
	}

	var filters []interface{}

	if !isServiceName(parentRefName) && env.Spec.Routing.Mode != "subdomain" {
		filters = append(filters, map[string]interface{}{
			"type": "RequestHeaderModifier",
			"requestHeaderModifier": map[string]interface{}{
				"remove": []interface{}{headerKey},
			},
		})
	}

	if env.Spec.Routing.Cookie != nil && env.Spec.Routing.Cookie.Enabled && kind == "HTTPRoute" && env.Spec.Routing.Mode != "subdomain" {
		maxAge := 86400
		if env.Spec.Routing.Cookie.MaxAge > 0 {
			maxAge = env.Spec.Routing.Cookie.MaxAge
		}
		sameSite := "Lax"
		if env.Spec.Routing.Cookie.SameSite != "" {
			sameSite = env.Spec.Routing.Cookie.SameSite
		}

		cookieValue := fmt.Sprintf("%s=%s; Path=/; Max-Age=%d; SameSite=%s", headerKey, headerValue, maxAge, sameSite)
		if sameSite == "None" || env.Spec.Routing.Cookie.Secure {
			cookieValue += "; Secure"
		}

		filters = append(filters, map[string]interface{}{
			"type": "ResponseHeaderModifier",
			"responseHeaderModifier": map[string]interface{}{
				"add": []interface{}{
					map[string]interface{}{
						"name":  "Set-Cookie",
						"value": cookieValue,
					},
				},
			},
		})
	}

	if len(filters) > 0 {
		rule["filters"] = filters
	}

	var rules []interface{}
	if cfg := env.Spec.ServiceConfig; cfg != nil && (cfg.ServiceName == "" || cfg.ServiceName == svc) && cfg.WebSocket != nil && cfg.WebSocket.Enabled && kind == "HTTPRoute" {
		timeout := cfg.WebSocket.Timeout
		if timeout == "" {
			timeout = "0s" // disable timeout by default
		}

		if cfg.WebSocket.Path != "" {
			wsPath := cfg.WebSocket.Path
			if cfg.PathPrefix != "" {
				wsPath = path.Join(cfg.PathPrefix, wsPath)
			}
			wsMatches := []interface{}{}
			for _, m := range matches {
				mMap := m.(map[string]interface{})
				wsMatch := map[string]interface{}{}
				for k, v := range mMap {
					wsMatch[k] = v
				}
				wsMatch["path"] = map[string]interface{}{
					"type":  "PathPrefix",
					"value": wsPath,
				}
				wsMatches = append(wsMatches, wsMatch)
			}

			wsRule := map[string]interface{}{
				"matches": wsMatches,
				"backendRefs": []interface{}{
					map[string]interface{}{
						"name": fmt.Sprintf("%s-%s", env.Name, svc),
						"port": backendPort,
					},
				},
				"timeouts": map[string]interface{}{
					"request": timeout,
				},
			}
			if filters, ok := rule["filters"]; ok {
				wsRule["filters"] = filters
			}
			rules = append(rules, wsRule)
		} else {
			rule["timeouts"] = map[string]interface{}{
				"request": timeout,
			}
		}
	}
	rules = append(rules, rule)

	spec := map[string]interface{}{
		"parentRefs": []interface{}{parentRef},
		"rules":      rules,
	}
	if len(hostnames) > 0 {
		spec["hostnames"] = hostnames
	}

	existing := &unstructured.Unstructured{}
	existing.SetAPIVersion(apiVersion)
	existing.SetKind(kind)

	err := r.Client.Get(ctx, client.ObjectKey{Name: routeName, Namespace: ns}, existing)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get %s for %s: %w", kind, svc, err)
		}
		u.Object["spec"] = spec
		if err := r.Client.Create(ctx, u); err != nil {
			return fmt.Errorf("failed to create %s for %s: %w", kind, svc, err)
		}
		logger.V(1).Info(fmt.Sprintf("Created %s", kind), "name", routeName, "service", svc)
	} else {
		existing.Object["spec"] = spec
		if err := r.Client.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed to update %s for %s: %w", kind, svc, err)
		}
		logger.V(1).Info(fmt.Sprintf("Updated %s", kind), "name", routeName, "service", svc)
	}

	oppositeKind := "GRPCRoute"
	oppositeAPIVersion := "gateway.networking.k8s.io/v1alpha2"
	if kind == "GRPCRoute" {
		oppositeKind = "HTTPRoute"
		oppositeAPIVersion = "gateway.networking.k8s.io/v1"
	}

	stale := &unstructured.Unstructured{}
	stale.SetAPIVersion(oppositeAPIVersion)
	stale.SetKind(oppositeKind)
	stale.SetName(routeName)
	stale.SetNamespace(ns)
	_ = r.Client.Delete(ctx, stale)

	return nil
}

// reconcileHTTPRoute creates or updates a single HTTPRoute.
func (r *GatewayRouter) reconcileHTTPRoute(ctx context.Context, env *v1alpha1.Environment, routeName, svc, ns, parentRefName, headerKey, headerValue string, backendPort int64) error {
	return r.reconcileRoute(ctx, env, "HTTPRoute", "gateway.networking.k8s.io/v1", routeName, svc, ns, parentRefName, headerKey, headerValue, backendPort)
}

// reconcileGRPCRoute creates or updates a single GRPCRoute for gRPC services.
func (r *GatewayRouter) reconcileGRPCRoute(ctx context.Context, env *v1alpha1.Environment, routeName, svc, ns, parentRefName, headerKey, headerValue string, backendPort int64) error {
	return r.reconcileRoute(ctx, env, "GRPCRoute", "gateway.networking.k8s.io/v1alpha2", routeName, svc, ns, parentRefName, headerKey, headerValue, backendPort)
}

// isServiceName heuristically determines if a parentRef name refers to a
// Kubernetes Service (for GAMMA mesh routing) vs a Gateway.
// Convention: Gateway names contain "gateway" or "waypoint".
func isServiceName(name string) bool {
	lower := strings.ToLower(name)
	return !strings.Contains(lower, "gateway") && !strings.Contains(lower, "waypoint")
}

// Teardown deletes all HTTPRoute and GRPCRoute resources associated with the
// environment by selecting on the diverge.io/environment label.
func (r *GatewayRouter) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	logger := log.FromContext(ctx).WithName("gateway-router")
	ns := r.namespace(env)

	selector := labels.SelectorFromSet(map[string]string{
		"diverge.io/environment": env.Name,
		"diverge.io/managed-by":  "diverge",
	})

	deleted := 0

	// Delete HTTPRoutes
	var httpRouteList unstructured.UnstructuredList
	httpRouteList.SetAPIVersion("gateway.networking.k8s.io/v1")
	httpRouteList.SetKind("HTTPRouteList")

	if err := r.Client.List(ctx, &httpRouteList,
		client.InNamespace(ns),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		return fmt.Errorf("failed to list HTTPRoutes for environment %s: %w", env.Name, err)
	}

	for i := range httpRouteList.Items {
		if err := r.Client.Delete(ctx, &httpRouteList.Items[i]); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete HTTPRoute %s: %w", httpRouteList.Items[i].GetName(), err)
			}
		}
		deleted++
	}

	// Delete GRPCRoutes
	var grpcRouteList unstructured.UnstructuredList
	grpcRouteList.SetAPIVersion("gateway.networking.k8s.io/v1alpha2")
	grpcRouteList.SetKind("GRPCRouteList")

	if err := r.Client.List(ctx, &grpcRouteList,
		client.InNamespace(ns),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		if meta.IsNoMatchError(err) {
			// GRPCRoute CRD not installed, skip cleanup
			return nil
		}
		return fmt.Errorf("listing GRPCRoutes: %w", err)
	}

	for i := range grpcRouteList.Items {
		if err := r.Client.Delete(ctx, &grpcRouteList.Items[i]); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete GRPCRoute %s: %w", grpcRouteList.Items[i].GetName(), err)
			}
		}
		deleted++
	}

	logger.Info("Tore down routes",
		"environment", env.Name,
		"deleted", deleted,
	)
	return nil
}

// GetExternalURL returns the environment's external URL by substituting
// {env} in the routing template, or an empty string if none is configured.
func (r *GatewayRouter) GetExternalURL(env *v1alpha1.Environment) string {
	if env.Spec.Routing.ExternalURL != "" {
		return strings.ReplaceAll(env.Spec.Routing.ExternalURL, "{env}", env.Name)
	}
	if env.Spec.Routing.Mode == "subdomain" && env.Spec.Routing.BaseDomain != "" {
		return fmt.Sprintf("https://%s.%s", env.Name, env.Spec.Routing.BaseDomain)
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
