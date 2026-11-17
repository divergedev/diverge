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
	"github.com/divergedev/diverge/internal/multicluster"
)

// IstioRouter implements Router using Istio AuthorizationPolicy resources.
type IstioRouter struct {
	Client         client.Client
	ClusterClients multicluster.Resolver
	scopedRemote   bool
}

func (r *IstioRouter) routerForEnv(ctx context.Context, env *v1alpha1.Environment) (*IstioRouter, error) {
	if r.scopedRemote || !multicluster.IsRemoteEnv(env) {
		return r, nil
	}
	resolver := r.ClusterClients
	if resolver == nil && r.Client != nil {
		resolver = multicluster.NewManager(r.Client, nil, nil, r.Client.Scheme())
	}
	if resolver == nil {
		return nil, fmt.Errorf("multi-cluster resolver is not configured")
	}
	target, err := resolver.Resolve(ctx, env)
	if err != nil {
		return nil, err
	}
	return &IstioRouter{
		Client:       target.Client,
		scopedRemote: true,
	}, nil
}

var _ Router = (*IstioRouter)(nil)

// Reconcile creates or updates an AuthorizationPolicy for the environment.
func (r *IstioRouter) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	scoped, err := r.routerForEnv(ctx, env)
	if err != nil {
		return fmt.Errorf("failed to resolve target cluster for istio routing: %w", err)
	}
	if scoped != r {
		return scoped.Reconcile(ctx, env)
	}

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

	if !r.scopedRemote && targetNS == env.Namespace {
		u.SetOwnerReferences([]metav1.OwnerReference{
			*metav1.NewControllerRef(env, v1alpha1.GroupVersion.WithKind("Environment")),
		})
	}

	u.SetLabels(map[string]string{
		"divergedev.com/managed-by":  "diverge",
		"divergedev.com/environment": env.Name,
	})

	ipBlocks := []interface{}{}
	if env.Spec.Routing.DevIP != "" {
		ip, err := netip.ParseAddr(env.Spec.Routing.DevIP)
		if err != nil {
			return fmt.Errorf("invalid DevIP %q: %w", env.Spec.Routing.DevIP, err)
		}
		prefix := netip.PrefixFrom(ip, ip.BitLen())
		ipBlocks = append(ipBlocks, prefix.String())
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

	principals := []interface{}{
		fmt.Sprintf("cluster.local/ns/%s/sa/*", targetNS),
	}
	if env.Namespace != targetNS && env.Namespace != "" {
		principals = append(principals, fmt.Sprintf("cluster.local/ns/%s/sa/*", env.Namespace))
	}
	// Allow ingress gateway principals to deliver routed traffic into the preview
	principals = append(principals, "cluster.local/ns/istio-system/sa/*")

	rules = append(rules, map[string]interface{}{
		"from": []interface{}{
			map[string]interface{}{
				"source": map[string]interface{}{
					"principals": principals,
				},
			},
		},
	})

	spec := map[string]interface{}{
		"action": "ALLOW",
		"rules":  rules,
		"selector": map[string]interface{}{
			"matchLabels": map[string]interface{}{
				"divergedev.com/environment": env.Name,
			},
		},
	}

	u.Object["spec"] = spec

	if err := r.Client.Apply(ctx, client.ApplyConfigurationFromUnstructured(u), client.ForceOwnership, client.FieldOwner("diverge")); err != nil {
		return fmt.Errorf("failed to apply AuthorizationPolicy: %w", err)
	}

	logger.Info("Reconciled AuthorizationPolicy", "name", policyName)
	return nil
}

// Teardown deletes AuthorizationPolicy resources by label selector.
func (r *IstioRouter) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	scoped, err := r.routerForEnv(ctx, env)
	if err != nil {
		if _, isVal := multicluster.IsValidationError(err); isVal && !multicluster.HasConfirmedRemoteTarget(env) {
			return nil
		}
		return fmt.Errorf("failed to resolve target cluster for istio teardown: %w", err)
	}
	if scoped != r {
		return scoped.Teardown(ctx, env)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	logger := log.FromContext(ctx).WithName("istio-router")

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	selector := labels.SelectorFromSet(map[string]string{
		"divergedev.com/environment": env.Name,
		"divergedev.com/managed-by":  "diverge",
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
