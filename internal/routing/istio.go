package routing

import (
	"context"
	"fmt"
	"net/netip"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// IstioRouter implements Router using Istio AuthorizationPolicy resources.
type IstioRouter struct {
	Client client.Client
}

var _ Router = (*IstioRouter)(nil)

// Reconcile creates or updates an AuthorizationPolicy for the environment.
func (r *IstioRouter) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	logger := log.FromContext(ctx).WithName("istio-router")

	if errs := validation.IsValidLabelValue(env.Name); len(errs) > 0 {
		return fmt.Errorf("invalid environment name %q: %v", env.Name, errs)
	}

	policyName := fmt.Sprintf("diverge-dev-%s", env.Name)
	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("security.istio.io/v1")
	u.SetKind("AuthorizationPolicy")
	u.SetName(policyName)
	u.SetNamespace(targetNS)

	u.SetOwnerReferences([]metav1.OwnerReference{
		*metav1.NewControllerRef(env, v1alpha1.GroupVersion.WithKind("Environment")),
	})

	u.SetLabels(map[string]string{
		"diverge.io/managed-by":  "diverge",
		"diverge.io/environment": env.Name,
	})

	ipBlocks := []interface{}{}
	if env.Spec.Routing.DevIP != "" {
		ip, err := netip.ParseAddr(env.Spec.Routing.DevIP)
		if err != nil {
			return fmt.Errorf("invalid DevIP %q: %w", env.Spec.Routing.DevIP, err)
		}
		ipBlocks = append(ipBlocks, fmt.Sprintf("%s/32", ip.String()))
	}

	rules := []interface{}{}
	if len(ipBlocks) > 0 {
		rules = append(rules, map[string]interface{}{
			"from": []interface{}{
				map[string]interface{}{
					"source": map[string]interface{}{
						"ipBlocks": ipBlocks,
					},
				},
			},
		})
	}

	rules = append(rules, map[string]interface{}{
		"from": []interface{}{
			map[string]interface{}{
				"source": map[string]interface{}{
					"principals": []interface{}{
						fmt.Sprintf("cluster.local/ns/%s/sa/*", targetNS),
					},
				},
			},
		},
	})

	spec := map[string]interface{}{
		"action": "ALLOW",
		"rules":  rules,
		"selector": map[string]interface{}{
			"matchLabels": map[string]interface{}{
				"diverge.io/environment": env.Name,
			},
		},
	}

	u.Object["spec"] = spec

	if err := r.Client.Patch(ctx, u, client.Apply, client.ForceOwnership, client.FieldOwner("diverge")); err != nil {
		return fmt.Errorf("failed to apply AuthorizationPolicy: %w", err)
	}

	logger.Info("Reconciled AuthorizationPolicy", "name", policyName)
	return nil
}

// Teardown deletes AuthorizationPolicy resources by label selector.
func (r *IstioRouter) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	logger := log.FromContext(ctx).WithName("istio-router")

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	selector := labels.SelectorFromSet(map[string]string{
		"diverge.io/environment": env.Name,
		"diverge.io/managed-by":  "diverge",
	})

	var policyList unstructured.UnstructuredList
	policyList.SetAPIVersion("security.istio.io/v1")
	policyList.SetKind("AuthorizationPolicyList")

	if err := r.Client.List(ctx, &policyList,
		client.InNamespace(targetNS),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		if meta.IsNoMatchError(err) {
			return nil
		}
		return fmt.Errorf("failed to list AuthorizationPolicies: %w", err)
	}

	deleted := 0
	for i := range policyList.Items {
		if err := r.Client.Delete(ctx, &policyList.Items[i]); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete AuthorizationPolicy %s: %w", policyList.Items[i].GetName(), err)
			}
			continue
		}
		deleted++
	}

	logger.Info("Tore down Istio policies", "environment", env.Name, "deleted", deleted)
	return nil
}

// GetExternalURL returns the environment's external URL.
func (r *IstioRouter) GetExternalURL(env *v1alpha1.Environment) string {
	return ""
}
