package routing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// TestGatewayAPIConformance_Cilium verifies Gateway API compliance with Cilium GatewayClass specifications.
func TestGatewayAPIConformance_Cilium(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{
		Client:    c,
		Namespace: "diverge-previews",
	}

	// 1. Ingress Header-based routing
	envHeader := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-cilium-header",
			Namespace: "diverge-previews",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"orders-svc"},
			},
			Routing: v1alpha1.EnvironmentRouting{
				Mode:        "header",
				HeaderKey:   "x-diverge-env",
				HeaderValue: "pr-cilium-header",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "orders-svc",
				Port:        8080,
				ParentRef:   "cilium-gateway",
				PathPrefix:  "/api/orders",
			},
		},
	}

	err := r.Reconcile(ctx, envHeader)
	require.NoError(t, err)

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1")
	u.SetKind("HTTPRoute")
	err = c.Get(ctx, client.ObjectKey{Name: "pr-cilium-header-orders-svc", Namespace: "diverge-previews"}, u)
	require.NoError(t, err)

	// Verify Cilium-compliant parentRef
	parentRefs, found, err := unstructured.NestedSlice(u.Object, "spec", "parentRefs")
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, parentRefs)
	pRef := parentRefs[0].(map[string]interface{})
	assert.Equal(t, "cilium-gateway", pRef["name"])

	// Verify Header + PathPrefix matches
	rules, found, err := unstructured.NestedSlice(u.Object, "spec", "rules")
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, rules)

	matches, found, err := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
	require.NoError(t, err)
	require.True(t, found)
	match := matches[0].(map[string]interface{})

	// Path prefix match
	pathMap, found, err := unstructured.NestedMap(match, "path")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "PathPrefix", pathMap["type"])
	assert.Equal(t, "/api/orders", pathMap["value"])

	// Header match
	headers, found, err := unstructured.NestedSlice(match, "headers")
	require.NoError(t, err)
	require.True(t, found)
	headerMatch := headers[0].(map[string]interface{})
	assert.Equal(t, "x-diverge-env", headerMatch["name"])
	assert.Equal(t, "pr-cilium-header", headerMatch["value"])
	assert.Equal(t, "Exact", headerMatch["type"])

	// 2. Subdomain routing
	envSubdomain := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-cilium-subdomain",
			Namespace: "diverge-previews",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"frontend-svc"},
			},
			Routing: v1alpha1.EnvironmentRouting{
				Mode:       "subdomain",
				BaseDomain: "preview.cilium.example.com",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "frontend-svc",
				Port:        3000,
				ParentRef:   "cilium-gateway",
			},
		},
	}

	err = r.Reconcile(ctx, envSubdomain)
	require.NoError(t, err)

	uSub := &unstructured.Unstructured{}
	uSub.SetAPIVersion("gateway.networking.k8s.io/v1")
	uSub.SetKind("HTTPRoute")
	err = c.Get(ctx, client.ObjectKey{Name: "pr-cilium-subdomain-frontend-svc", Namespace: "diverge-previews"}, uSub)
	require.NoError(t, err)

	hostnames, found, err := unstructured.NestedStringSlice(uSub.Object, "spec", "hostnames")
	require.NoError(t, err)
	require.True(t, found)
	assert.Contains(t, hostnames, "pr-cilium-subdomain.preview.cilium.example.com")
}

// TestGatewayAPIConformance_Linkerd_GAMMA verifies east-west GAMMA mesh routing specification with parentRef: Service.
func TestGatewayAPIConformance_Linkerd_GAMMA(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{
		Client:    c,
		Namespace: "linkerd-mesh-ns",
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-linkerd-gamma",
			Namespace: "linkerd-mesh-ns",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"inventory-svc"},
			},
			Routing: v1alpha1.EnvironmentRouting{
				Mode:        "header",
				HeaderKey:   "x-diverge-env",
				HeaderValue: "pr-linkerd-gamma",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "inventory-svc",
				Port:        9090,
				ParentRef:   "linkerd-gateway",
			},
		},
	}

	err := r.Reconcile(ctx, env)
	require.NoError(t, err)

	// In GAMMA, mesh route name is <envName>-<svc>-mesh
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1")
	u.SetKind("HTTPRoute")
	err = c.Get(ctx, client.ObjectKey{Name: "pr-linkerd-gamma-inventory-svc-mesh", Namespace: "linkerd-mesh-ns"}, u)
	require.NoError(t, err)

	// GAMMA specification: parentRefs must point to the frontend Kubernetes Service
	parentRefs, found, err := unstructured.NestedSlice(u.Object, "spec", "parentRefs")
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, parentRefs)

	pRef := parentRefs[0].(map[string]interface{})
	assert.Equal(t, "inventory-svc", pRef["name"])
	assert.Equal(t, "Service", pRef["kind"])
	group, _ := pRef["group"].(string)
	assert.True(t, group == "" || group == "core")

	// Verify backendRefs point to preview service
	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	backendRefs, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "backendRefs")
	require.NotEmpty(t, backendRefs)
	bRef := backendRefs[0].(map[string]interface{})
	assert.Equal(t, "pr-linkerd-gamma-inventory-svc", bRef["name"])
	assert.EqualValues(t, 9090, bRef["port"])

	// Verify Teardown removes both HTTPRoutes (ingress and mesh)
	err = r.Teardown(ctx, env)
	require.NoError(t, err)

	err = c.Get(ctx, client.ObjectKey{Name: "pr-linkerd-gamma-inventory-svc-mesh", Namespace: "linkerd-mesh-ns"}, u)
	assert.Error(t, err)
}

// TestGatewayAPIConformance_GRPCRoute verifies GRPCRoute creation for gRPC services under Gateway API.
func TestGatewayAPIConformance_GRPCRoute(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{
		Client:    c,
		Namespace: "default",
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-grpc",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"grpc-backend"},
			},
			Routing: v1alpha1.EnvironmentRouting{
				Mode:        "header",
				HeaderKey:   "x-diverge-env",
				HeaderValue: "pr-grpc",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "grpc-backend",
				Port:        50051,
				ParentRef:   "main-gateway",
				Protocol:    "grpc",
			},
		},
	}

	err := r.Reconcile(ctx, env)
	require.NoError(t, err)

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1alpha2")
	u.SetKind("GRPCRoute")
	err = c.Get(ctx, client.ObjectKey{Name: "pr-grpc-grpc-backend", Namespace: "default"}, u)
	require.NoError(t, err)

	// Verify GRPCRoute header match
	rules, found, err := unstructured.NestedSlice(u.Object, "spec", "rules")
	require.NoError(t, err)
	require.True(t, found)
	matches, found, err := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
	require.NoError(t, err)
	require.True(t, found)
	headers, found, err := unstructured.NestedSlice(matches[0].(map[string]interface{}), "headers")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "x-diverge-env", headers[0].(map[string]interface{})["name"])
	assert.Equal(t, "pr-grpc", headers[0].(map[string]interface{})["value"])
}
