//go:build e2e && e2e_cilium

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/divergedev/diverge/api/v1alpha1"
)

const ciliumGatewayFallbackURL = "http://localhost:8880"

func resolveCiliumGatewayURL(ctx context.Context, f *Framework) string {
	if override := strings.TrimSpace(os.Getenv("CILIUM_GATEWAY_URL")); override != "" {
		return override
	}
	svc, err := f.Clientset.CoreV1().Services("default").Get(ctx, "cilium-gateway-diverge-gateway", metav1.GetOptions{})
	if err == nil && len(svc.Spec.Ports) > 0 && svc.Spec.Ports[0].NodePort > 0 {
		nodes, nErr := f.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if nErr == nil {
			for _, node := range nodes.Items {
				for _, addr := range node.Status.Addresses {
					if addr.Type == corev1.NodeInternalIP && addr.Address != "" {
						return fmt.Sprintf("http://%s:%d", addr.Address, svc.Spec.Ports[0].NodePort)
					}
				}
			}
		}
	}
	return ciliumGatewayFallbackURL
}

// TestCilium_HeaderRouting verifies that Cilium routes requests to preview
// pods when the x-diverge-env header matches the environment name.
func TestCilium_HeaderRouting(t *testing.T) {
	f := NewFramework(t)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	f.CreateNamespace(ctx)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		f.CleanupNamespace(cleanupCtx)
	}()

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping data-plane assertions")
	}

	// Deploy baseline echo service
	f.DeployEchoServer(ctx, "api-svc", f.Namespace, 5678)

	// Create Environment with header-mode routing
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cilium-header",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/cilium-header",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "api-svc",
				Port:        5678,
				Image:       "hashicorp/http-echo:0.2.3",
				Env:         []v1alpha1.EnvVar{{Name: "ECHO_TEXT", Value: "preview"}},
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err)

	// Wait for HTTPRoute to be created
	require.Eventually(t, func() bool {
		var routes gatewayv1.HTTPRouteList
		if err := f.Client.List(ctx, &routes, client.InNamespace(f.Namespace)); err != nil {
			return false
		}
		return len(routes.Items) > 0
	}, 2*time.Minute, 2*time.Second, "HTTPRoute not created")

	// Verify route uses header matching
	var routes gatewayv1.HTTPRouteList
	err = f.Client.List(ctx, &routes, client.InNamespace(f.Namespace))
	require.NoError(t, err)
	require.NotEmpty(t, routes.Items)

	route := routes.Items[0]
	require.NotEmpty(t, route.Spec.Rules)
	require.NotEmpty(t, route.Spec.Rules[0].Matches)
	match := route.Spec.Rules[0].Matches[0]
	require.NotEmpty(t, match.Headers)
	assert.Equal(t, "x-diverge-env", string(match.Headers[0].Name))

	// Data-plane: send request with header and verify routing
	err = f.WaitForRouteReachable(ctx, RequestOpts{
		GatewayURL: resolveCiliumGatewayURL(ctx, f),
		Headers:    map[string]string{"x-diverge-env": "cilium-header"},
	}, 90*time.Second)
	require.NoError(t, err, "Route not reachable through Cilium gateway")
}

// TestCilium_SubdomainRouting verifies that Cilium routes requests based
// on the Host header for subdomain-mode routing.
func TestCilium_SubdomainRouting(t *testing.T) {
	f := NewFramework(t)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	f.CreateNamespace(ctx)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		f.CleanupNamespace(cleanupCtx)
	}()

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping data-plane assertions")
	}

	f.DeployEchoServer(ctx, "web-svc", f.Namespace, 5678)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cilium-subdomain",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/cilium-subdomain",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "web-svc",
				Port:        5678,
				Image:       "hashicorp/http-echo:0.2.3",
				Env:         []v1alpha1.EnvVar{{Name: "ECHO_TEXT", Value: "preview"}},
			},
			Routing: v1alpha1.EnvironmentRouting{
				Mode:       "subdomain",
				BaseDomain: "preview.local",
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err)

	// Wait for HTTPRoute with hostname
	require.Eventually(t, func() bool {
		var routes gatewayv1.HTTPRouteList
		if err := f.Client.List(ctx, &routes, client.InNamespace(f.Namespace)); err != nil {
			return false
		}
		for _, r := range routes.Items {
			if len(r.Spec.Hostnames) > 0 {
				return true
			}
		}
		return false
	}, 2*time.Minute, 2*time.Second, "HTTPRoute with hostname not created")

	// Verify hostname in route
	var routes gatewayv1.HTTPRouteList
	err = f.Client.List(ctx, &routes, client.InNamespace(f.Namespace))
	require.NoError(t, err)

	found := false
	for _, r := range routes.Items {
		for _, h := range r.Spec.Hostnames {
			if string(h) == "cilium-subdomain.preview.local" {
				found = true
			}
		}
	}
	assert.True(t, found, "Expected hostname cilium-subdomain.preview.local in HTTPRoute")

	// Data-plane: verify subdomain routing reaches preview pod
	err = f.WaitForRouteReachable(ctx, RequestOpts{
		GatewayURL: resolveCiliumGatewayURL(ctx, f),
		Host:       "cilium-subdomain.preview.local",
	}, 90*time.Second)
	require.NoError(t, err, "Subdomain route not reachable through Cilium gateway")
}

// TestCilium_GAMMAMeshRouting verifies east-west routing via GAMMA
// (HTTPRoute with parentRef: Service).
func TestCilium_GAMMAMeshRouting(t *testing.T) {
	f := NewFramework(t)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	f.CreateNamespace(ctx)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		f.CleanupNamespace(cleanupCtx)
	}()

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping GAMMA assertions")
	}

	f.DeployEchoServer(ctx, "payments-svc", f.Namespace, 5678)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cilium-gamma",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/cilium-gamma",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "payments-svc",
				Port:        5678,
				Image:       "hashicorp/http-echo:0.2.3",
				Env:         []v1alpha1.EnvVar{{Name: "ECHO_TEXT", Value: "preview"}},
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err)

	// Wait for mesh HTTPRoute (parentRef with kind: Service)
	require.Eventually(t, func() bool {
		var routes gatewayv1.HTTPRouteList
		if err := f.Client.List(ctx, &routes, client.InNamespace(f.Namespace)); err != nil {
			return false
		}
		for _, r := range routes.Items {
			for _, ref := range r.Spec.ParentRefs {
				if ref.Kind != nil && string(*ref.Kind) == "Service" {
					return true
				}
			}
		}
		return false
	}, 2*time.Minute, 2*time.Second, "GAMMA mesh HTTPRoute not created")

	// Deploy in-cluster client for GAMMA requests
	err = f.DeployInClusterClient(ctx, "gamma-client", f.Namespace)
	require.NoError(t, err)

	// Wait for east-west routing to be programmed
	require.Eventually(t, func() bool {
		out, err := f.SendMeshRequest(ctx, "gamma-client", f.Namespace, "payments-svc", 5678, map[string]string{
			"x-diverge-env": "cilium-gamma",
		})
		if err != nil {
			return false
		}
		// A successful connection through the mesh with a non-empty response
		// proves the east-west GAMMA route is programmed correctly.
		return len(out) > 0
	}, 2*time.Minute, 2*time.Second, "Mesh route not reachable")
}

// TestCilium_CrossNamespaceGrant verifies ReferenceGrant creation for
// cross-namespace HTTPRoute → Service binding.
func TestCilium_CrossNamespaceGrant(t *testing.T) {
	f := NewFramework(t)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	f.CreateNamespace(ctx)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		f.CleanupNamespace(cleanupCtx)
	}()

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping cross-namespace assertions")
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cilium-crossns",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/cilium-crossns",
			},
			Deploy: v1alpha1.EnvironmentDeploy{
				Namespace: "create",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "external-svc",
				Port:        5678,
				Image:       "hashicorp/http-echo:0.2.3",
				Env:         []v1alpha1.EnvVar{{Name: "ECHO_TEXT", Value: "preview"}},
			},
		},
	}
	previewNS := env.PreviewNamespace()

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err)

	// Wait for ReferenceGrant in the isolated preview namespace
	var grants unstructured.UnstructuredList
	grants.SetAPIVersion("gateway.networking.k8s.io/v1beta1")
	grants.SetKind("ReferenceGrantList")
	require.Eventually(t, func() bool {
		if err := f.Client.List(ctx, &grants, client.InNamespace(previewNS)); err != nil {
			return false
		}
		return len(grants.Items) > 0
	}, 2*time.Minute, 2*time.Second, "ReferenceGrant not created in preview namespace")

	_ = f.Client.Delete(ctx, env)
	_ = f.WaitForEnvironmentDeleted(ctx, env.Name, 2*time.Minute)
}

// TestCilium_RouteCleanup verifies that HTTPRoute resources are garbage
// collected when the Environment is deleted.
func TestCilium_RouteCleanup(t *testing.T) {
	f := NewFramework(t)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	f.CreateNamespace(ctx)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		f.CleanupNamespace(cleanupCtx)
	}()

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping cleanup assertions")
	}

	f.DeployEchoServer(ctx, "cleanup-svc", f.Namespace, 5678)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cilium-cleanup",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/cilium-cleanup",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "cleanup-svc",
				Port:        5678,
				Image:       "hashicorp/http-echo:0.2.3",
				Env:         []v1alpha1.EnvVar{{Name: "ECHO_TEXT", Value: "preview"}},
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err)

	// Wait for HTTPRoute
	require.Eventually(t, func() bool {
		var routes gatewayv1.HTTPRouteList
		if err := f.Client.List(ctx, &routes, client.InNamespace(f.Namespace)); err != nil {
			return false
		}
		return len(routes.Items) > 0
	}, 2*time.Minute, 2*time.Second)

	// Delete environment
	err = f.Client.Delete(ctx, env)
	require.NoError(t, err)
	err = f.WaitForEnvironmentDeleted(ctx, env.Name, 2*time.Minute)
	require.NoError(t, err)

	// Poll until HTTPRoutes are cleaned up
	require.Eventually(t, func() bool {
		var routes gatewayv1.HTTPRouteList
		if err := f.Client.List(ctx, &routes, client.InNamespace(f.Namespace)); err != nil {
			return false
		}
		return len(routes.Items) == 0
	}, 30*time.Second, 1*time.Second, "HTTPRoutes should be cleaned up after Environment deletion")
}

// TestCilium_CookieRouting verifies HTTPRoute creation with a cookie match.
func TestCilium_CookieRouting(t *testing.T) {
	f := NewFramework(t)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	f.CreateNamespace(ctx)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		f.CleanupNamespace(cleanupCtx)
	}()

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping data-plane assertions")
	}

	f.DeployEchoServer(ctx, "payments-svc", f.Namespace, 5678)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cilium-cookie",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/cilium-cookie",
			},
			Routing: v1alpha1.EnvironmentRouting{
				Mode: "header",
				Cookie: &v1alpha1.CookieSpec{
					Enabled: true,
				},
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "payments-svc",
				Port:        5678,
				Image:       "hashicorp/http-echo:0.2.3",
				Env:         []v1alpha1.EnvVar{{Name: "ECHO_TEXT", Value: "preview"}},
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err)

	// Wait for HTTPRoute to be created
	require.Eventually(t, func() bool {
		var routes gatewayv1.HTTPRouteList
		if err := f.Client.List(ctx, &routes, client.InNamespace(f.Namespace)); err != nil {
			return false
		}
		return len(routes.Items) > 0
	}, 2*time.Minute, 2*time.Second, "HTTPRoute not created")

	var routes gatewayv1.HTTPRouteList
	err = f.Client.List(ctx, &routes, client.InNamespace(f.Namespace))
	require.NoError(t, err)
	// Verify Cookie header match across OR match entries on the Gateway HTTPRoute
	var route gatewayv1.HTTPRoute
	for _, item := range routes.Items {
		if !strings.HasSuffix(item.Name, "-mesh") {
			route = item
			break
		}
	}
	if route.Name == "" {
		route = routes.Items[0]
	}

	require.NotEmpty(t, route.Spec.Rules)
	require.NotEmpty(t, route.Spec.Rules[0].Matches)

	var cookieMatch *gatewayv1.HTTPHeaderMatch
	for _, m := range route.Spec.Rules[0].Matches {
		for _, h := range m.Headers {
			if strings.EqualFold(string(h.Name), "cookie") {
				hdr := h
				cookieMatch = &hdr
				break
			}
		}
		if cookieMatch != nil {
			break
		}
	}
	require.NotNil(t, cookieMatch, "No Cookie header match found")
	require.Equal(t, gatewayv1.HeaderMatchRegularExpression, *cookieMatch.Type)

	// Data-plane: send request with Cookie
	err = f.WaitForRouteReachable(ctx, RequestOpts{
		GatewayURL: resolveCiliumGatewayURL(ctx, f),
		Headers:    map[string]string{"Cookie": "x-diverge-env=cilium-cookie"},
	}, 90*time.Second)
	require.NoError(t, err, "Route not reachable through Cilium gateway via Cookie")
}

// TestCilium_PathPrefixRouting verifies HTTPRoute creation with a PathPrefix match.
func TestCilium_PathPrefixRouting(t *testing.T) {
	f := NewFramework(t)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	f.CreateNamespace(ctx)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		f.CleanupNamespace(cleanupCtx)
	}()

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping data-plane assertions")
	}

	f.DeployEchoServer(ctx, "payments-svc", f.Namespace, 5678)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cilium-path",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/cilium-path",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "payments-svc",
				Port:        5678,
				Image:       "hashicorp/http-echo:0.2.3",
				Env:         []v1alpha1.EnvVar{{Name: "ECHO_TEXT", Value: "preview"}},
				PathPrefix:  "/api",
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err)

	// Wait for HTTPRoute to be created
	require.Eventually(t, func() bool {
		var routes gatewayv1.HTTPRouteList
		if err := f.Client.List(ctx, &routes, client.InNamespace(f.Namespace)); err != nil {
			return false
		}
		return len(routes.Items) > 0
	}, 2*time.Minute, 2*time.Second, "HTTPRoute not created")

	var routes gatewayv1.HTTPRouteList
	err = f.Client.List(ctx, &routes, client.InNamespace(f.Namespace))
	require.NoError(t, err)
	route := routes.Items[0]

	// Verify PathPrefix match
	require.NotEmpty(t, route.Spec.Rules)
	require.NotEmpty(t, route.Spec.Rules[0].Matches)
	match := route.Spec.Rules[0].Matches[0]
	require.NotNil(t, match.Path)
	require.Equal(t, gatewayv1.PathMatchPathPrefix, *match.Path.Type)
	require.Equal(t, "/api", *match.Path.Value)

	// Data-plane: send request with matching path and header
	err = f.WaitForRouteReachable(ctx, RequestOpts{
		GatewayURL: resolveCiliumGatewayURL(ctx, f),
		Path:       "/api",
		Headers:    map[string]string{"x-diverge-env": "cilium-path"},
	}, 90*time.Second)
	require.NoError(t, err, "Route not reachable through Cilium gateway via PathPrefix")
}

// TestCilium_GRPCRouteCreation verifies GRPCRoute creation.
func TestCilium_GRPCRouteCreation(t *testing.T) {
	f := NewFramework(t)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	f.CreateNamespace(ctx)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		f.CleanupNamespace(cleanupCtx)
	}()

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping data-plane assertions")
	}

	f.DeployEchoServer(ctx, "grpc-svc", f.Namespace, 50051)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cilium-grpc",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/cilium-grpc",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "grpc-svc",
				Port:        50051,
				Image:       "hashicorp/http-echo:0.2.3",
				Env:         []v1alpha1.EnvVar{{Name: "ECHO_TEXT", Value: "preview"}},
				Protocol:    "grpc",
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err)

	// Verify GRPCRoute (not HTTPRoute) is created
	require.Eventually(t, func() bool {
		var grpcRouteList unstructured.UnstructuredList
		grpcRouteList.SetAPIVersion("gateway.networking.k8s.io/v1")
		grpcRouteList.SetKind("GRPCRouteList")
		if err := f.Client.List(ctx, &grpcRouteList, client.InNamespace(f.Namespace)); err != nil {
			return false
		}
		return len(grpcRouteList.Items) > 0
	}, 2*time.Minute, 2*time.Second, "GRPCRoute not created")

	var grpcRouteList unstructured.UnstructuredList
	grpcRouteList.SetAPIVersion("gateway.networking.k8s.io/v1")
	grpcRouteList.SetKind("GRPCRouteList")
	err = f.Client.List(ctx, &grpcRouteList, client.InNamespace(f.Namespace))
	require.NoError(t, err)

	route := grpcRouteList.Items[0]
	// Verify header matching in GRPCRoute unstructured object
	rules, found, err := unstructured.NestedSlice(route.Object, "spec", "rules")
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, rules)

	rule := rules[0].(map[string]interface{})
	matches, found, err := unstructured.NestedSlice(rule, "matches")
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, matches)

	match := matches[0].(map[string]interface{})
	headers, found, err := unstructured.NestedSlice(match, "headers")
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, headers)

	headerMatch := headers[0].(map[string]interface{})
	assert.Equal(t, "x-diverge-env", headerMatch["name"])
	assert.Equal(t, "cilium-grpc", headerMatch["value"])
}
