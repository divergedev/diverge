package routing

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// GRPCRouter implements Router using Gateway API GRPCRoute resources.
type GRPCRouter struct {
	client    client.Client
	namespace string
}

var _ Router = (*GRPCRouter)(nil)

func NewGRPCRouter(c client.Client, namespace string) *GRPCRouter {
	return &GRPCRouter{
		client:    c,
		namespace: namespace,
	}
}

func (r *GRPCRouter) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	headerKey := env.Spec.Routing.HeaderKey
	if headerKey == "" {
		headerKey = "x-diverge-env"
	}

	headerValue := env.Spec.Routing.HeaderValue
	if headerValue == "" {
		headerValue = env.Name
	}

	for _, svc := range env.Spec.Deploy.ChangedServices {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion("gateway.networking.k8s.io/v1")
		u.SetKind("GRPCRoute")
		u.SetName(fmt.Sprintf("%s-%s", env.Name, svc))
		u.SetNamespace(r.namespace)

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
		existing.SetKind("GRPCRoute")

		err := r.client.Get(ctx, client.ObjectKey{Name: u.GetName(), Namespace: u.GetNamespace()}, existing)
		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to get GRPCRoute for %s: %w", svc, err)
			}
			u.Object["spec"] = spec
			if err := r.client.Create(ctx, u); err != nil {
				return fmt.Errorf("failed to create GRPCRoute for %s: %w", svc, err)
			}
		} else {
			existing.Object["spec"] = spec
			if err := r.client.Update(ctx, existing); err != nil {
				return fmt.Errorf("failed to update GRPCRoute for %s: %w", svc, err)
			}
		}
	}

	return nil
}

func (r *GRPCRouter) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	for _, svc := range env.Spec.Deploy.ChangedServices {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion("gateway.networking.k8s.io/v1")
		u.SetKind("GRPCRoute")
		u.SetName(fmt.Sprintf("%s-%s", env.Name, svc))
		u.SetNamespace(r.namespace)

		if err := r.client.Delete(ctx, u); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete GRPCRoute for %s: %w", svc, err)
			}
		}
	}
	return nil
}

func (r *GRPCRouter) GetExternalURL(env *v1alpha1.Environment) string {
	if env.Spec.Routing.ExternalURL != "" {
		return strings.ReplaceAll(env.Spec.Routing.ExternalURL, "{env}", env.Name)
	}
	return ""
}
