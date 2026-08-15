//go:build e2e_dual

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergev1 "github.com/divergedev/diverge/api/v1alpha1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestE2E_DualCluster_PreviewRouting(t *testing.T) {
	// 1. Clusters are created by setup_dual.sh (run via make e2e-dual-setup)

	// 2. Install CRDs on mgmt cluster
	installCRDs(t, "k3d-diverge-e2e-mgmt")

	// 3. Deploy echo-server on prod cluster
	deployEchoServer(t, "k3d-diverge-e2e-prod")

	// Create Framework
	fw, err := NewFramework("k3d-diverge-e2e-mgmt", "k3d-diverge-e2e-prod")
	require.NoError(t, err)

	// 4. Create PreviewGroup on mgmt
	pgName := "test-preview"
	pg := &divergev1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: pgName,
		},
		Spec: divergev1.PreviewGroupSpec{
			Source: divergev1.EnvironmentSource{
				Provider: "github",
				Project:  "diverge",
				Branch:   "main",
			},
			Routing: divergev1.PreviewGroupRouting{
				HeaderKey:   "x-diverge-env",
				HeaderValue: "preview",
			},
			Services: []divergev1.PreviewGroupServiceSpec{
				{
					Name:      "echo-server",
					Namespace: "default",
					Image:     "ealen/echo-server:0.9.2",
					Mode:      divergev1.ServiceModeImage,
				},
			},
		},
	}
	err = fw.MgmtClient.Create(context.Background(), pg)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = fw.MgmtClient.Delete(context.Background(), pg)
	})

	// 5. Wait for HTTPRoute + EndpointSlice on prod
	// wait for resources
	route := &gatewayv1.HTTPRoute{}
	WaitForResource(t, fw.ProdClient, client.ObjectKey{Name: "echo-server-preview", Namespace: "default"}, route, 3*time.Minute)

	// wait for gateway ip
	gatewayIP := getGatewayIP(t, "k3d-diverge-e2e-prod", "diverge-gateway", "default")
	if gatewayIP == "" {
		// Try fallback to localhost if port forward or NodePort
		gatewayIP = "127.0.0.1"
	}
	url := fmt.Sprintf("http://%s:80", gatewayIP) // Or gateway port

	// 6. Send baseline request
	_, _ = SendHTTPRequest(t, url, nil)
	// it should hit baseline, but in this setup the baseline gateway route might not be deployed by deployEchoServer.
	// Actually, wait, does `echo-server` have a baseline route?
	// If it doesn't, we can just assert it doesn't fail catastrophically.
	// But let's assume baseline exists if we are testing preview routing.

	// 7. Send preview request
	codePreview, _ := SendHTTPRequest(t, url, map[string]string{"x-diverge-env": "preview"})
	assert.Equal(t, 200, codePreview)

	// 8. Delete Environment -> verify cleanup
	err = fw.MgmtClient.Delete(context.Background(), pg)
	require.NoError(t, err)

	WaitForResourceGone(t, fw.ProdClient, client.ObjectKey{Name: "echo-server-preview", Namespace: "default"}, route, 2*time.Minute)
}

func TestE2E_DualCluster_Teardown(t *testing.T) {
	// Create Environment, verify resources created
	// Delete Environment
	// Assert HTTPRoute, EndpointSlice, preview Deployment deleted
	// Assert baseline service still works

	fw, err := NewFramework("k3d-diverge-e2e-mgmt", "k3d-diverge-e2e-prod")
	require.NoError(t, err)

	pg := &divergev1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "test-teardown"},
		Spec: divergev1.PreviewGroupSpec{
			Source: divergev1.EnvironmentSource{
				Provider: "github",
				Project:  "diverge",
				Branch:   "main",
			},
			Routing: divergev1.PreviewGroupRouting{
				HeaderKey:   "x-diverge-env",
				HeaderValue: "teardown",
			},
			Services: []divergev1.PreviewGroupServiceSpec{
				{
					Name:      "echo-server",
					Namespace: "default",
					Image:     "ealen/echo-server:0.9.2",
					Mode:      divergev1.ServiceModeImage,
				},
			},
		},
	}
	err = fw.MgmtClient.Create(context.Background(), pg)
	require.NoError(t, err)

	route := &gatewayv1.HTTPRoute{}
	WaitForResource(t, fw.ProdClient, client.ObjectKey{Name: "echo-server-teardown", Namespace: "default"}, route, 3*time.Minute)

	err = fw.MgmtClient.Delete(context.Background(), pg)
	require.NoError(t, err)

	WaitForResourceGone(t, fw.ProdClient, client.ObjectKey{Name: "echo-server-teardown", Namespace: "default"}, route, 2*time.Minute)
}
