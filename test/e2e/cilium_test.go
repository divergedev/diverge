//go:build e2e && e2e_cilium

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

const ciliumGatewayURL = "http://localhost:8880"

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
	f.DeployEchoServer(ctx, "api-svc", f.Namespace, 8080)

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
				Port:        8080,
				Image:       "hashicorp/http-echo:0.2.3",
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
		GatewayURL: ciliumGatewayURL,
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

	f.DeployEchoServer(ctx, "web-svc", f.Namespace, 8080)

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
				Port:        8080,
				Image:       "hashicorp/http-echo:0.2.3",
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
		GatewayURL: ciliumGatewayURL,
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

	f.DeployEchoServer(ctx, "payments-svc", f.Namespace, 8080)

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
				Port:        8080,
				Image:       "hashicorp/http-echo:0.2.3",
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

	// Create the target namespace that the cross-namespace route will reference
	targetNS := f.Namespace + "-target"
	f.CreateNamespaceByName(ctx, targetNS)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		f.CleanupNamespaceByName(cleanupCtx, targetNS)
	}()

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
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "external-svc",
				Namespace:   targetNS,
				Port:        8080,
				Image:       "hashicorp/http-echo:0.2.3",
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err)

	// Wait for ReferenceGrant in the target namespace
	var grants gatewayv1.ReferenceGrantList
	require.Eventually(t, func() bool {
		if err := f.Client.List(ctx, &grants, client.InNamespace(targetNS)); err != nil {
			return false
		}
		return len(grants.Items) > 0
	}, 2*time.Minute, 2*time.Second, "ReferenceGrant not created in target namespace")
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

	f.DeployEchoServer(ctx, "cleanup-svc", f.Namespace, 8080)

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
				Port:        8080,
				Image:       "hashicorp/http-echo:0.2.3",
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
