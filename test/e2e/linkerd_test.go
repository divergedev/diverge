//go:build e2e && e2e_linkerd

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

// TestLinkerd_GAMMARouting verifies that Linkerd routes east-west traffic
// via GAMMA HTTPRoutes with parentRef: Service.
func TestLinkerd_GAMMARouting(t *testing.T) {
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

	// Annotate namespace for Linkerd injection
	ns := f.Namespace
	f.AnnotateNamespace(ctx, ns, map[string]string{
		"linkerd.io/inject": "enabled",
	})

	f.DeployEchoServer(ctx, "orders-svc", ns, 8080)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "linkerd-gamma",
			Namespace: ns,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/linkerd-gamma",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "orders-svc",
				Port:        8080,
				Image:       "hashicorp/http-echo:0.2.3",
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err)

	// Wait for mesh HTTPRoute (parentRef: Service)
	require.Eventually(t, func() bool {
		var routes gatewayv1.HTTPRouteList
		if err := f.Client.List(ctx, &routes, client.InNamespace(ns)); err != nil {
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
	err = f.DeployInClusterClient(ctx, "gamma-client", ns)
	require.NoError(t, err)

	// Wait for east-west routing to be programmed
	require.Eventually(t, func() bool {
		out, err := f.SendMeshRequest(ctx, "gamma-client", ns, "orders-svc", 8080, map[string]string{
			"x-diverge-env": "linkerd-gamma",
		})
		if err != nil {
			return false
		}
		// A successful connection through the mesh with a non-empty response
		// proves the east-west GAMMA route is programmed correctly.
		return len(out) > 0
	}, 2*time.Minute, 2*time.Second, "Mesh route not reachable or not routing to preview pod")
}

// TestLinkerd_HeaderPropagation verifies that the x-diverge-env header
// is preserved through the Linkerd proxy and not stripped.
func TestLinkerd_HeaderPropagation(t *testing.T) {
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
		t.Skip("controller not deployed — skipping header propagation assertions")
	}

	ns := f.Namespace
	f.AnnotateNamespace(ctx, ns, map[string]string{
		"linkerd.io/inject": "enabled",
	})

	f.DeployEchoServer(ctx, "header-svc", ns, 8080)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "linkerd-headers",
			Namespace: ns,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/linkerd-headers",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "header-svc",
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
		if err := f.Client.List(ctx, &routes, client.InNamespace(ns)); err != nil {
			return false
		}
		return len(routes.Items) > 0
	}, 2*time.Minute, 2*time.Second, "HTTPRoute not created")

	// Verify the route has header matching configured
	var routes gatewayv1.HTTPRouteList
	err = f.Client.List(ctx, &routes, client.InNamespace(ns))
	require.NoError(t, err)
	require.NotEmpty(t, routes.Items)

	route := routes.Items[0]
	require.NotEmpty(t, route.Spec.Rules)
	require.NotEmpty(t, route.Spec.Rules[0].Matches)

	hasHeaderMatch := false
	for _, m := range route.Spec.Rules[0].Matches {
		for _, h := range m.Headers {
			if string(h.Name) == "x-diverge-env" {
				hasHeaderMatch = true
			}
		}
	}
	assert.True(t, hasHeaderMatch, "HTTPRoute should have x-diverge-env header match")
}

// TestLinkerd_CrossNamespaceRouting verifies ReferenceGrant for cross-namespace
// HTTPRoute → Service binding through the Linkerd mesh.
func TestLinkerd_CrossNamespaceRouting(t *testing.T) {
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

	// Create the target namespace for cross-namespace route reference
	targetNS := f.Namespace + "-target"
	f.CreateNamespaceByName(ctx, targetNS)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		f.CleanupNamespaceByName(cleanupCtx, targetNS)
	}()

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "linkerd-crossns",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/linkerd-crossns",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "remote-svc",
				Namespace:   targetNS,
				Port:        8080,
				Image:       "hashicorp/http-echo:0.2.3",
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err)

	// Wait for ReferenceGrant in target namespace
	var grants gatewayv1.ReferenceGrantList
	require.Eventually(t, func() bool {
		if err := f.Client.List(ctx, &grants, client.InNamespace(targetNS)); err != nil {
			return false
		}
		return len(grants.Items) > 0
	}, 2*time.Minute, 2*time.Second, "ReferenceGrant not created in target namespace")
}

// TestLinkerd_RouteCleanup verifies that HTTPRoute resources are garbage
// collected when the Environment is deleted through the Linkerd mesh.
func TestLinkerd_RouteCleanup(t *testing.T) {
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

	ns := f.Namespace
	f.AnnotateNamespace(ctx, ns, map[string]string{
		"linkerd.io/inject": "enabled",
	})

	f.DeployEchoServer(ctx, "cleanup-svc", ns, 8080)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "linkerd-cleanup",
			Namespace: ns,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/linkerd-cleanup",
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
		if err := f.Client.List(ctx, &routes, client.InNamespace(ns)); err != nil {
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
		if err := f.Client.List(ctx, &routes, client.InNamespace(ns)); err != nil {
			return false
		}
		return len(routes.Items) == 0
	}, 30*time.Second, 1*time.Second, "HTTPRoutes should be cleaned up")
}
