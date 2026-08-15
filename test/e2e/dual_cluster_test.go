//go:build e2e_dual

package e2e

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergev1 "github.com/divergedev/diverge/api/v1alpha1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// childEnvironmentName mirrors the controller's naming logic.
func childEnvironmentName(groupName, serviceName string) string {
	raw := fmt.Sprintf("pg-%s-%s", groupName, serviceName)
	raw = strings.ToLower(raw)
	raw = strings.NewReplacer(".", "-", "_", "-").Replace(raw)
	raw = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(raw, "")
	raw = regexp.MustCompile(`-{2,}`).ReplaceAllString(raw, "-")
	raw = strings.Trim(raw, "-")

	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(groupName+"/"+serviceName)))[:8]

	if len(raw) <= 63-9 {
		return raw + "-" + hash
	}
	return raw[:63-9] + "-" + hash
}

// httpRouteName computes the HTTPRoute name the GatewayRouter will create.
func httpRouteName(groupName, serviceName string) string {
	envName := childEnvironmentName(groupName, serviceName)
	return fmt.Sprintf("%s-%s", envName, serviceName)
}

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
	svcName := "echo-server"
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
					Name:      svcName,
					Namespace: "default",
					Image:     "ealen/echo-server:0.9.2",
					Mode:      divergev1.ServiceModeImage,
					Env: []divergev1.EnvVar{
						{Name: "ECHO_MSG", Value: "preview"},
					},
				},
			},
		},
	}
	err = fw.MgmtClient.Create(context.Background(), pg)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = fw.MgmtClient.Delete(context.Background(), pg)
	})

	// 5. Wait for the child Environment to be created
	envName := childEnvironmentName(pgName, svcName)
	env := &divergev1.Environment{}
	WaitForResource(t, fw.MgmtClient, client.ObjectKey{Name: envName, Namespace: "default"}, env, 3*time.Minute)
	t.Logf("Environment created: %s", envName)

	// 6. Wait for HTTPRoute (named <envName>-<svcName>)
	routeName := httpRouteName(pgName, svcName)
	route := &gatewayv1.HTTPRoute{}
	WaitForResource(t, fw.MgmtClient, client.ObjectKey{Name: routeName, Namespace: "default"}, route, 3*time.Minute)
	t.Logf("HTTPRoute created: %s", routeName)

	// 7. Wait for gateway IP
	gatewayIP := getGatewayIP(t, "k3d-diverge-e2e-mgmt", "diverge-gateway", "default")
	if gatewayIP == "" {
		t.Skip("Gateway not reachable")
	}
	url := fmt.Sprintf("http://%s:80", gatewayIP)

	// 8. Send baseline request
	codeBase, bodyBase := SendHTTPRequest(t, url, nil)
	assert.Equal(t, 200, codeBase)
	assert.NotContains(t, bodyBase, "\"ECHO_MSG\":\"preview\"")

	// 9. Send preview request
	codePreview, bodyPreview := SendHTTPRequest(t, url, map[string]string{"x-diverge-env": "preview"})
	assert.Equal(t, 200, codePreview)
	assert.Contains(t, bodyPreview, "\"ECHO_MSG\":\"preview\"")

	// 10. Delete PreviewGroup -> verify cleanup
	err = fw.MgmtClient.Delete(context.Background(), pg)
	require.NoError(t, err)

	WaitForResourceGone(t, fw.MgmtClient, client.ObjectKey{Name: routeName, Namespace: "default"}, route, 2*time.Minute)
}

func TestE2E_DualCluster_Teardown(t *testing.T) {
	fw, err := NewFramework("k3d-diverge-e2e-mgmt", "k3d-diverge-e2e-prod")
	require.NoError(t, err)

	pgName := "test-teardown"
	svcName := "echo-server"
	pg := &divergev1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: pgName},
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
					Name:      svcName,
					Namespace: "default",
					Image:     "ealen/echo-server:0.9.2",
					Mode:      divergev1.ServiceModeImage,
				},
			},
		},
	}
	err = fw.MgmtClient.Create(context.Background(), pg)
	require.NoError(t, err)

	routeName := httpRouteName(pgName, svcName)
	route := &gatewayv1.HTTPRoute{}
	WaitForResource(t, fw.MgmtClient, client.ObjectKey{Name: routeName, Namespace: "default"}, route, 3*time.Minute)

	err = fw.MgmtClient.Delete(context.Background(), pg)
	require.NoError(t, err)

	WaitForResourceGone(t, fw.MgmtClient, client.ObjectKey{Name: routeName, Namespace: "default"}, route, 2*time.Minute)
}
