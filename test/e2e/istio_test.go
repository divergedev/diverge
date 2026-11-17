//go:build e2e && e2e_istio

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func deployAmbientWaypoint(t *testing.T, ctx context.Context, f *Framework, ns string) {
	t.Helper()
	waypoint := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "waypoint",
			Namespace: ns,
			Labels: map[string]string{
				"istio.io/waypoint-for": "service",
			},
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "istio-waypoint",
			Listeners: []gatewayv1.Listener{
				{
					Name:     "mesh",
					Port:     15008,
					Protocol: gatewayv1.ProtocolType("HBONE"),
				},
			},
		},
	}
	require.NoError(t, f.Client.Create(ctx, waypoint))

	require.Eventually(t, func() bool {
		var gw gatewayv1.Gateway
		if err := f.Client.Get(ctx, client.ObjectKey{Name: "waypoint", Namespace: ns}, &gw); err != nil {
			return false
		}
		for _, cond := range gw.Status.Conditions {
			if cond.Type == string(gatewayv1.GatewayConditionProgrammed) && cond.Status == metav1.ConditionTrue {
				return true
			}
		}
		return false
	}, 2*time.Minute, 2*time.Second, "Istio Ambient waypoint Gateway did not become Programmed")
}

// TestIstio_HeaderRouting verifies that Istio Ambient + Waypoint routes east-west
// L7 traffic via GAMMA HTTPRoutes with parentRef: Service, preserving baseline
// responses for unrouted traffic while splitting x-diverge-env traffic to the preview pod.
func TestIstio_HeaderRouting(t *testing.T) {
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

	// Enroll namespace in Istio Ambient mesh + namespace Waypoint proxy
	ns := f.Namespace
	f.LabelNamespace(ctx, ns, map[string]string{
		"istio.io/dataplane-mode": "ambient",
		"istio.io/use-waypoint":   "waypoint",
	})
	deployAmbientWaypoint(t, ctx, f, ns)

	f.DeployEchoServer(ctx, "orders-svc", ns, 5678)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "istio-routing",
			Namespace: ns,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/istio-routing",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "orders-svc",
				Port:        5678,
				Image:       "hashicorp/http-echo:1.0.0",
				Env:         []v1alpha1.EnvVar{{Name: "ECHO_TEXT", Value: "preview"}},
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

	// 1. Verify header-matched east-west traffic routes through Waypoint to the preview pod ("preview").
	require.Eventually(t, func() bool {
		out, err := f.SendMeshRequest(ctx, "gamma-client", ns, "orders-svc", 5678, map[string]string{
			"x-diverge-env": "istio-routing",
		})
		if err != nil {
			return false
		}
		return strings.TrimSpace(out) == "preview"
	}, 2*time.Minute, 2*time.Second, "Istio Ambient Waypoint GAMMA route did not route x-diverge-env traffic to preview pod")

	// 2. Verify unrouted east-west traffic continues reaching baseline orders-svc ("orders-svc").
	require.Eventually(t, func() bool {
		out, err := f.SendMeshRequest(ctx, "gamma-client", ns, "orders-svc", 5678, nil)
		if err != nil {
			return false
		}
		return strings.TrimSpace(out) == "orders-svc"
	}, 1*time.Minute, 2*time.Second, "Baseline east-west traffic without x-diverge-env did not reach orders-svc")
}

// TestIstio_HeaderPropagation verifies that the x-diverge-env header
// is preserved through the Istio proxy and not stripped.
func TestIstio_HeaderPropagation(t *testing.T) {
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
	f.LabelNamespace(ctx, ns, map[string]string{
		"istio.io/dataplane-mode": "ambient",
	})

	f.DeployEchoServer(ctx, "header-svc", ns, 5678)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "istio-headers",
			Namespace: ns,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/istio-headers",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "header-svc",
				Port:        5678,
				Image:       "hashicorp/http-echo:1.0.0",
				Env:         []v1alpha1.EnvVar{{Name: "ECHO_TEXT", Value: "preview"}},
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

// TestIstio_RouteCleanup verifies that HTTPRoute resources are garbage
// collected when the Environment is deleted through the Istio mesh.
func TestIstio_RouteCleanup(t *testing.T) {
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
	f.LabelNamespace(ctx, ns, map[string]string{
		"istio.io/dataplane-mode": "ambient",
	})

	f.DeployEchoServer(ctx, "cleanup-svc", ns, 5678)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "istio-cleanup",
			Namespace: ns,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/istio-cleanup",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "cleanup-svc",
				Port:        5678,
				Image:       "hashicorp/http-echo:1.0.0",
				Env:         []v1alpha1.EnvVar{{Name: "ECHO_TEXT", Value: "preview"}},
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
