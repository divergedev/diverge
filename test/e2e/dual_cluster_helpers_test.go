//go:build e2e_dual

package e2e

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func createK3dCluster(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "k3d", "cluster", "create", name,
		"--no-lb",
		"--k3s-arg", "--disable=traefik@server:0",
		"--wait",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "failed to create k3d cluster %s", name)

	t.Cleanup(func() {
		delCmd := exec.Command("k3d", "cluster", "delete", name)
		delCmd.Run() // best-effort cleanup
	})
}

func installCRDs(t *testing.T, contextName string) {
	t.Helper()
	// Note: Path assumes test runs from diverge root or we need relative paths.
	// We will use "../../config/crd/bases/" since test runs in test/e2e/
	cmd := exec.Command("kubectl", "apply", "--context="+contextName, "-f", "../../config/crd/bases/")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "failed to install CRDs: %s", string(out))
}

func deployEchoServer(t *testing.T, contextName string) {
	t.Helper()

	yamlContent := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: echo-server
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: echo-server
  template:
    metadata:
      labels:
        app: echo-server
    spec:
      containers:
      - name: echo-server
        image: ealen/echo-server:0.9.2
        ports:
        - containerPort: 80
---
apiVersion: v1
kind: Service
metadata:
  name: echo-server
  namespace: default
spec:
  selector:
    app: echo-server
  ports:
    - protocol: TCP
      port: 80
      targetPort: 80
`
	applyManifest(t, contextName, yamlContent)

	// Wait for Ready
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: contextName},
	).ClientConfig()
	require.NoError(t, err)

	clientset, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	WaitForPodReady(t, clientset, "default", "app=echo-server", 2*time.Minute)
}

func applyManifest(t *testing.T, contextName, yamlContent string) {
	t.Helper()
	cmd := exec.Command("kubectl", "apply", "--context="+contextName, "-f", "-")
	cmd.Stdin = strings.NewReader(yamlContent)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "failed to apply manifest: %s", string(out))
}

func deleteManifest(t *testing.T, contextName, yamlContent string) {
	t.Helper()
	cmd := exec.Command("kubectl", "delete", "--context="+contextName, "-f", "-")
	cmd.Stdin = strings.NewReader(yamlContent)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "failed to delete manifest: %s", string(out))
}

func getGatewayIP(t *testing.T, contextName, gatewayName, namespace string) string {
	t.Helper()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: contextName},
	).ClientConfig()
	require.NoError(t, err)

	_, err = kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	var ip string
	WaitForCondition(t, 2*time.Minute, 2*time.Second, func() (bool, error) {
		// Get gateway via kubectl since Gateway API is dynamically typed in go if we don't have generated clients,
		// or we can use dynamic client. But simpler with kubectl.
		cmd := exec.Command("kubectl", "get", "gateway", gatewayName, "-n", namespace, "--context="+contextName, "-o", "jsonpath={.status.addresses[0].value}")
		out, err := cmd.Output()
		if err == nil && len(out) > 0 {
			ip = string(out)
			return true, nil
		}
		return false, nil
	})
	return ip
}
