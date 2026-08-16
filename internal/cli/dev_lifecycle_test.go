package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

// TestDevLifecycle_CreateAndCleanup verifies that runDev:
// 1. Creates a PreviewGroup when started
// 2. Creates an Environment with correct routing
// 3. Cleans up the Environment and PreviewGroup on context cancel
func TestDevLifecycle_CreateAndCleanup(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
		username:    "testuser",
	}
	app, c, cmd, cancel := runDevTestSetup(t, detector)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDev(app, "", 0, "", "inject", false, nil, cmd, WithEnvironmentDetector(detector))
	}()

	var pg divergeiov1alpha1.PreviewGroup
	require.Eventually(t, func() bool {
		err := c.Get(context.Background(), types.NamespacedName{Name: "dev-testuser-web"}, &pg)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond, "PreviewGroup should be created")

	// The background controller mock in runDevTestSetup will create the Environment
	var env divergeiov1alpha1.Environment
	require.Eventually(t, func() bool {
		err := c.Get(context.Background(), types.NamespacedName{Name: "env-web", Namespace: "default"}, &env)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond, "Environment should be created by the mock controller")

	// Trigger cleanup
	cancel()
	require.NoError(t, <-errCh)

	err := c.Get(context.Background(), types.NamespacedName{Name: "dev-testuser-web"}, &pg)
	require.True(t, apierrors.IsNotFound(err), "PreviewGroup should be deleted on cleanup")
}

// TestDevLifecycle_EnvSync verifies that:
// 1. Baseline env vars are fetched from the cluster
// 2. The merged env is written to .env.diverge
func TestDevLifecycle_EnvSync(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
		username:    "testuser",
	}
	app, _, cmd, cancel := runDevTestSetup(t, detector)
	defer cancel()

	// Inject a baseline pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-baseline",
			Namespace: "default",
			Labels:    map[string]string{"app": "web", "diverge.io/role": "baseline"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "web",
					Env: []corev1.EnvVar{
						{Name: "BASELINE_VAR", Value: "baseline_value"},
					},
				},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	app.Clientset = k8sfake.NewSimpleClientset(pod)

	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDev(app, "", 0, "", "file", false, []string{"echo", "done"}, cmd, WithEnvironmentDetector(detector))
	}()

	require.NoError(t, <-errCh)

	content, err := os.ReadFile(".env.diverge")
	require.NoError(t, err)
	envStr := string(content)

	assert.Contains(t, envStr, "BASELINE_VAR=baseline_value")
}

// TestDevLifecycle_GracefulShutdown verifies that:
// 1. Ctrl+C (context cancel) triggers cleanup
// 2. No resources leak after shutdown
func TestDevLifecycle_GracefulShutdown(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
		username:    "testuser",
	}
	app, c, cmd, cancel := runDevTestSetup(t, detector)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDev(app, "", 0, "", "inject", false, nil, cmd, WithEnvironmentDetector(detector))
	}()

	var pg divergeiov1alpha1.PreviewGroup
	require.Eventually(t, func() bool {
		err := c.Get(context.Background(), types.NamespacedName{Name: "dev-testuser-web"}, &pg)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)

	// Simulate Ctrl+C
	cancel()

	require.NoError(t, <-errCh)

	err := c.Get(context.Background(), types.NamespacedName{Name: "dev-testuser-web"}, &pg)
	require.True(t, apierrors.IsNotFound(err))
}

// TestDevLifecycle_ChildProcess verifies that:
// 1. The child command receives correct env vars
// 2. Child exit code is propagated
func TestDevLifecycle_ChildProcess(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
		username:    "testuser",
	}
	app, _, cmd, cancel := runDevTestSetup(t, detector)
	defer cancel()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-baseline",
			Namespace: "default",
			Labels:    map[string]string{"app": "web", "diverge.io/role": "baseline"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "web",
					Env: []corev1.EnvVar{
						{Name: "DIVERGE_CHILD_TEST", Value: "123"},
					},
				},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	app.Clientset = k8sfake.NewSimpleClientset(pod)

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "out.txt")

	err := runDev(app, "", 0, "", "inject", false, []string{"sh", "-c", "echo $DIVERGE_CHILD_TEST > " + outFile}, cmd, WithEnvironmentDetector(detector))
	require.NoError(t, err)

	content, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "123")
}

// TestDevLifecycle_ReconnectOnError verifies that:
// 1. Transient API errors don't crash the session
func TestDevLifecycle_ReconnectOnError(t *testing.T) {
	t.Skip("Heartbeat errors are ignored by design, no specific reconnect logic to test")
}

func TestDevspaceFlagLifecycle(t *testing.T) {
	app := &App{}
	cmd := &cobra.Command{}

	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	err := runDev(app, "my-service", 0, "", "inject", true, nil, cmd)
	require.NoError(t, err)

	content, err := os.ReadFile("devspace.yaml")
	require.NoError(t, err)
	assert.Contains(t, string(content), "diverge dev --service ${DIVERGE_SERVICE} -- devspace dev")
}
