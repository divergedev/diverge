//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func getKubeClient(t *testing.T) (client.Client, *kubernetes.Clientset) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{CurrentContext: "k3d-oneazra-dev"}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := kubeConfig.ClientConfig()
	require.NoError(t, err)

	cs, err := kubernetes.NewForConfig(config)
	require.NoError(t, err)

	c, err := client.New(config, client.Options{})
	require.NoError(t, err)
	err = divergeiov1alpha1.AddToScheme(c.Scheme())
	require.NoError(t, err)

	return c, cs
}

func buildCLI(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "../../bin/diverge", "../../cmd/diverge")
	err := cmd.Run()
	require.NoError(t, err, "failed to build diverge CLI")
}

func TestE2E_DevCreatesPreviewGroup(t *testing.T) {
	buildCLI(t)
	c, _ := getKubeClient(t)
	ctx := context.Background()

	// Start diverge dev in background
	cmd := exec.Command("../../bin/diverge", "dev", "--service", "echo-server", "--endpoint", "127.0.0.1:9999", "--port", "9999", "--namespace", "diverge-e2e-test")
	err := cmd.Start()
	require.NoError(t, err)

	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGINT)
			_ = cmd.Wait()
		}
	}()

	// Wait for PreviewGroup
	var pgList divergeiov1alpha1.PreviewGroupList
	require.Eventually(t, func() bool {
		err := c.List(ctx, &pgList)
		if err != nil {
			return false
		}
		for _, pg := range pgList.Items {
			for _, svc := range pg.Spec.Services {
				if svc.Name == "echo-server" && svc.Endpoint == "127.0.0.1:9999" && svc.Mode == divergeiov1alpha1.ServiceModeLocal {
					return true
				}
			}
		}
		return false
	}, 15*time.Second, 1*time.Second, "PreviewGroup should be created")

	// Send SIGINT
	err = cmd.Process.Signal(syscall.SIGINT)
	require.NoError(t, err)
	_ = cmd.Wait() // Wait for cleanup to finish

	// Verify cleanup
	require.Eventually(t, func() bool {
		err := c.List(ctx, &pgList)
		require.NoError(t, err)
		for _, pg := range pgList.Items {
			for _, svc := range pg.Spec.Services {
				if svc.Name == "echo-server" {
					return false
				}
			}
		}
		return true
	}, 15*time.Second, 1*time.Second, "PreviewGroup should be cleaned up")
}

func TestE2E_HeadlessServiceAndEndpointSlice(t *testing.T) {
	t.Skip("requires diverge controller to be running")
}

func TestE2E_DevInterceptAndRelease(t *testing.T) {
	buildCLI(t)
	c, _ := getKubeClient(t)
	ctx := context.Background()

	// 1. Create PreviewGroup
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-group",
			Namespace: "default", // CLI defaults to namespace default usually, but controller puts cluster scope
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{
					Name: "echo-server",
					Mode: divergeiov1alpha1.ServiceModeImage,
				},
			},
		},
	}
	err := c.Create(ctx, pg)
	require.NoError(t, err)
	defer func() {
		_ = c.Delete(ctx, pg)
	}()

	// 2. Intercept
	cmd := exec.Command("../../bin/diverge", "preview", "intercept", "echo-server", "--group", "test-group", "--endpoint", "127.0.0.1:8080")
	err = cmd.Run()
	require.NoError(t, err)

	// 3. Verify
	var updatedPg divergeiov1alpha1.PreviewGroup
	err = c.Get(ctx, client.ObjectKey{Name: "test-group", Namespace: "default"}, &updatedPg) // or cluster scoped?
	// Note: PreviewGroup is likely cluster-scoped, so Namespace=""
	err = c.Get(ctx, client.ObjectKey{Name: "test-group"}, &updatedPg)
	require.NoError(t, err)
	assert.Equal(t, divergeiov1alpha1.ServiceModeLocal, updatedPg.Spec.Services[0].Mode)
	assert.Equal(t, "127.0.0.1:8080", updatedPg.Spec.Services[0].Endpoint)

	// 4. Release
	cmd = exec.Command("../../bin/diverge", "preview", "release", "echo-server", "--group", "test-group")
	err = cmd.Run()
	require.NoError(t, err)

	// 5. Verify
	err = c.Get(ctx, client.ObjectKey{Name: "test-group"}, &updatedPg)
	require.NoError(t, err)
	assert.Equal(t, divergeiov1alpha1.ServiceModeImage, updatedPg.Spec.Services[0].Mode)
	assert.Empty(t, updatedPg.Spec.Services[0].Endpoint)
}
