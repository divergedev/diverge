package cli

import (
	"context"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestDevCmd_InterceptAndRelease(t *testing.T) {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)

	c := fake.NewClientBuilder().WithScheme(s).Build()
	app := &App{Client: c}

	// Create a PreviewGroup first
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "dev-test"},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "my-service", Mode: divergeiov1alpha1.ServiceModeImage},
			},
		},
	}
	require.NoError(t, c.Create(context.Background(), pg))

	// Intercept test
	cmd := newPreviewInterceptCmd(app)
	cmd.SetArgs([]string{"my-service", "--group", "dev-test", "--endpoint", "10.0.0.1:8080"})
	require.NoError(t, cmd.Execute())

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "dev-test"}, pg))
	assert.Equal(t, divergeiov1alpha1.ServiceModeLocal, pg.Spec.Services[0].Mode)
	assert.Equal(t, "10.0.0.1:8080", pg.Spec.Services[0].Endpoint)

	// Release test
	releaseCmd := newPreviewReleaseCmd(app)
	releaseCmd.SetArgs([]string{"my-service", "--group", "dev-test"})
	require.NoError(t, releaseCmd.Execute())

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "dev-test"}, pg))
	assert.Equal(t, divergeiov1alpha1.ServiceModeImage, pg.Spec.Services[0].Mode)
	assert.Empty(t, pg.Spec.Services[0].Endpoint)
}

func TestDevCmd_Intercept_MissingGroup(t *testing.T) {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)

	c := fake.NewClientBuilder().WithScheme(s).Build()
	app := &App{Client: c}

	cmd := newPreviewInterceptCmd(app)
	cmd.SetArgs([]string{"my-service", "--group", "nonexistent", "--endpoint", "10.0.0.1:8080"})
	err := cmd.Execute()
	assert.Error(t, err, "intercept should fail for nonexistent group")
}

func TestDevCmd_Release_MissingGroup(t *testing.T) {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)

	c := fake.NewClientBuilder().WithScheme(s).Build()
	app := &App{Client: c}

	cmd := newPreviewReleaseCmd(app)
	cmd.SetArgs([]string{"my-service", "--group", "nonexistent"})
	err := cmd.Execute()
	assert.Error(t, err, "release should fail for nonexistent group")
}

type fakeDetector struct {
	tailscaleIP  string
	tailscaleErr error
	gitBranch    string
	gitBranchErr error
	serviceName  string
	serviceErr   error
	username     string
}

func (f fakeDetector) DetectTailscaleIP() (string, error) {
	return f.tailscaleIP, f.tailscaleErr
}

func (f fakeDetector) DetectGitBranch() (string, error) {
	return f.gitBranch, f.gitBranchErr
}

func (f fakeDetector) DetectServiceName() (string, error) {
	return f.serviceName, f.serviceErr
}

func (f fakeDetector) DetectUsername() string {
	if f.username == "" {
		return "dev"
	}
	return f.username
}

func runDevTestSetup(t *testing.T, detector EnvironmentDetector) (*App, client.Client, *cobra.Command, context.CancelFunc) {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	app := &App{
		Client:    c,
		Clientset: k8sfake.NewSimpleClientset(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)

	return app, c, cmd, cancel
}

func TestRunDev_CreatesPreviewGroup(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		gitBranch:   "main",
		serviceName: "web",
		username:    "alice",
	}
	app, c, cmd, cancel := runDevTestSetup(t, detector)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDev(app, "", 0, "", cmd, WithEnvironmentDetector(detector))
	}()

	var pg divergeiov1alpha1.PreviewGroup
	require.Eventually(t, func() bool {
		err := c.Get(context.Background(), types.NamespacedName{Name: "dev-alice-web"}, &pg)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)

	require.Equal(t, "dev-alice-web", pg.Name)
	require.Len(t, pg.Spec.Services, 1)
	require.Equal(t, "web", pg.Spec.Services[0].Name)
	require.Equal(t, divergeiov1alpha1.ServiceModeLocal, pg.Spec.Services[0].Mode)
	require.Equal(t, "100.100.100.100:8080", pg.Spec.Services[0].Endpoint)
	require.Equal(t, "x-diverge-env", pg.Spec.Routing.HeaderKey)
	require.Equal(t, "main", pg.Spec.Routing.HeaderValue)
	require.Equal(t, "main", pg.Spec.Source.Branch)

	cancel()
	err := <-errCh
	require.NoError(t, err)

	err = c.Get(context.Background(), types.NamespacedName{Name: "dev-alice-web"}, &pg)
	require.Error(t, err)
}

func TestRunDev_UsesExplicitFlags(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		gitBranch:   "main",
		serviceName: "web",
		username:    "alice",
	}
	app, c, cmd, cancel := runDevTestSetup(t, detector)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDev(app, "backend", 9090, "10.0.0.1", cmd, WithEnvironmentDetector(detector))
	}()

	var pg divergeiov1alpha1.PreviewGroup
	require.Eventually(t, func() bool {
		err := c.Get(context.Background(), types.NamespacedName{Name: "dev-alice-backend"}, &pg)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)

	require.Equal(t, "backend", pg.Spec.Services[0].Name)
	require.Equal(t, "10.0.0.1:9090", pg.Spec.Services[0].Endpoint)

	cancel()
	err := <-errCh
	require.NoError(t, err)
}

func TestRunDev_TailscaleError(t *testing.T) {
	detector := fakeDetector{
		tailscaleErr: ErrTailscaleNotFound,
	}
	app, _, cmd, cancel := runDevTestSetup(t, detector)
	defer cancel()

	err := runDev(app, "web", 0, "", cmd, WithEnvironmentDetector(detector))
	require.ErrorIs(t, err, ErrTailscaleNotFound)
}

func TestRunDev_DefaultPort(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
	}
	app, c, cmd, cancel := runDevTestSetup(t, detector)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDev(app, "", 0, "", cmd, WithEnvironmentDetector(detector))
	}()

	var pg divergeiov1alpha1.PreviewGroup
	require.Eventually(t, func() bool {
		err := c.Get(context.Background(), types.NamespacedName{Name: "dev-dev-web"}, &pg)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, "100.100.100.100:8080", pg.Spec.Services[0].Endpoint)

	cancel()
	<-errCh
}

func TestRunDev_SlugifiesBranch(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		gitBranch:   "feat/my-feature",
		serviceName: "web",
	}
	app, c, cmd, cancel := runDevTestSetup(t, detector)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDev(app, "", 0, "", cmd, WithEnvironmentDetector(detector))
	}()

	var pg divergeiov1alpha1.PreviewGroup
	require.Eventually(t, func() bool {
		err := c.Get(context.Background(), types.NamespacedName{Name: "dev-dev-web"}, &pg)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, "feat-my-feature", pg.Spec.Routing.HeaderValue)
	require.Equal(t, "feat-my-feature", pg.Spec.Source.Branch)

	cancel()
	<-errCh
}

func TestRunDev_FallbackBranch(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP:  "100.100.100.100",
		gitBranchErr: ErrNoGitRepo,
		serviceName:  "web",
	}
	app, c, cmd, cancel := runDevTestSetup(t, detector)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDev(app, "", 0, "", cmd, WithEnvironmentDetector(detector))
	}()

	var pg divergeiov1alpha1.PreviewGroup
	require.Eventually(t, func() bool {
		err := c.Get(context.Background(), types.NamespacedName{Name: "dev-dev-web"}, &pg)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, "local-dev", pg.Spec.Routing.HeaderValue)

	cancel()
	<-errCh
}

func TestRunDev_GroupNameFormat(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "my_service",
		username:    "user_name",
	}
	app, c, cmd, cancel := runDevTestSetup(t, detector)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDev(app, "", 0, "", cmd, WithEnvironmentDetector(detector))
	}()

	var pg divergeiov1alpha1.PreviewGroup
	require.Eventually(t, func() bool {
		err := c.Get(context.Background(), types.NamespacedName{Name: "dev-user-name-my-service"}, &pg)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	<-errCh
}

func TestRunDev_CleanupOnContextCancel(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
	}
	app, c, cmd, cancel := runDevTestSetup(t, detector)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDev(app, "", 0, "", cmd, WithEnvironmentDetector(detector))
	}()

	var pg divergeiov1alpha1.PreviewGroup
	require.Eventually(t, func() bool {
		err := c.Get(context.Background(), types.NamespacedName{Name: "dev-dev-web"}, &pg)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	<-errCh

	err := c.Get(context.Background(), types.NamespacedName{Name: "dev-dev-web"}, &pg)
	require.Error(t, err)
}

func TestRunDev_ServiceNameFromConfig(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "detected-svc",
	}
	app, c, cmd, cancel := runDevTestSetup(t, detector)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDev(app, "", 0, "", cmd, WithEnvironmentDetector(detector))
	}()

	var pg divergeiov1alpha1.PreviewGroup
	require.Eventually(t, func() bool {
		err := c.Get(context.Background(), types.NamespacedName{Name: "dev-dev-detected-svc"}, &pg)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, "detected-svc", pg.Spec.Services[0].Name)

	cancel()
	<-errCh
}

func TestRunDev_CleansUpOnSignal(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
	}
	app, c, cmd, cancel := runDevTestSetup(t, detector)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDev(app, "", 0, "", cmd, WithEnvironmentDetector(detector))
	}()

	var pg divergeiov1alpha1.PreviewGroup
	require.Eventually(t, func() bool {
		err := c.Get(context.Background(), types.NamespacedName{Name: "dev-dev-web"}, &pg)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	err := <-errCh
	require.NoError(t, err)

	err = c.Get(context.Background(), types.NamespacedName{Name: "dev-dev-web"}, &pg)
	require.Error(t, err)
}

func TestRunDev_CleanupTimeout(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
	}

	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)

	c := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if deadline, ok := ctx.Deadline(); ok {
				remaining := time.Until(deadline)
				if remaining < 4*time.Second || remaining > 6*time.Second {
					t.Errorf("expected deadline ~5s from now, got %v", remaining)
				}
			} else {
				t.Errorf("expected deadline on context")
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}).Build()

	app := &App{
		Client:    c,
		Clientset: k8sfake.NewSimpleClientset(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDev(app, "", 0, "", cmd, WithEnvironmentDetector(detector))
	}()

	require.Eventually(t, func() bool {
		var pg divergeiov1alpha1.PreviewGroup
		err := c.Get(context.Background(), types.NamespacedName{Name: "dev-dev-web"}, &pg)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)

	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("cleanup timed out, likely missing context with timeout")
	}
}
