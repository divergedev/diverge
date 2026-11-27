package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/tunnelclient"
	"github.com/divergedev/diverge/pkg/devsession"
)

func TestRunDev_TunnelDNSValidation(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
		username:    "alice",
	}
	app, _, cmd, cancel := runDevTestSetup(t, detector)
	defer cancel()

	err := runDev(runDevParams{
		App:       app,
		Cmd:       cmd,
		NoTunnel:  false,
		PreviewID: "INVALID_PREVIEW",
		Server:    "http://dummy.local",
		Options:   []DevOption{WithEnvironmentDetector(detector)},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not a valid DNS label")
}

type mockTunnelServerTimeout struct {
	divergev1alpha1connect.UnimplementedTunnelServiceHandler
}

func (m *mockTunnelServerTimeout) Tunnel(ctx context.Context, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
	_, err := stream.Receive()
	if err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func TestRunDev_TunnelTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
		username:    "alice",
		gitBranch:   "main",
	}
	app, _, cmd, cancel := runDevTestSetup(t, detector)

	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(&mockTunnelServerTimeout{})
	mux.Handle(path, handler)
	ts := httptest.NewUnstartedServer(mux)
	ts.EnableHTTP2 = true
	ts.StartTLS()

	// Intercept default transport so the un-injected &http.Client{} trusts the test server
	origTransport := http.DefaultTransport
	http.DefaultTransport = ts.Client().Transport
	t.Cleanup(func() {
		cancel()
		ts.Close()
		http.DefaultTransport = origTransport
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDev(runDevParams{
			App:      app,
			Cmd:      cmd,
			NoTunnel: false,
			Server:   ts.URL,
			Token:    "test-token",
			Options:  []DevOption{WithEnvironmentDetector(detector)},
		})
	}()

	select {
	case err := <-errCh:
		require.Error(t, err)
		require.Contains(t, err.Error(), "tunnel connection timed out")
	case <-time.After(17 * time.Second):
		t.Fatal("test hung waiting for timeout")
	}
}

func TestRunDev_TunnelServerDiscoveryFailure(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
		username:    "alice",
		gitBranch:   "main",
	}
	app, _, cmd, cancel := runDevTestSetup(t, detector)
	defer cancel()

	// Provide a kubeconfig so RestConfig() succeeds and we actually reach discoverServer.
	// The fake clientset has no services, so discoverServer returns ErrServerNotFound.
	tmpKubeconfig, err := os.CreateTemp("", "kubeconfig-*")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpKubeconfig.Name()) }()
	// editorconfig-checker-disable
	_, err = tmpKubeconfig.WriteString(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:1
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user: {}
`)
	// editorconfig-checker-enable
	require.NoError(t, err)
	require.NoError(t, tmpKubeconfig.Close())
	app.Kubeconfig = tmpKubeconfig.Name()

	err = runDev(runDevParams{
		App:      app,
		Cmd:      cmd,
		NoTunnel: false,
		Server:   "",
		Options:  []DevOption{WithEnvironmentDetector(detector)},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to discover server")
}

// TestRunDev_TunnelNoCredential pins the actionable failure when no credential
// can be resolved. The server rejects an unauthenticated Tunnel RPC with 401,
// so failing here beats reconnecting into that forever.
func TestRunDev_TunnelNoCredential(t *testing.T) {
	t.Setenv(tunnelTokenEnvVar, "")
	t.Setenv("BAO_TOKEN", "")
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("HOME", t.TempDir())

	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
		username:    "alice",
		gitBranch:   "main",
	}
	app, _, cmd, cancel := runDevTestSetup(t, detector)
	defer cancel()

	tmpKubeconfig, err := os.CreateTemp("", "kubeconfig-*")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpKubeconfig.Name()) }()
	_, err = tmpKubeconfig.WriteString("apiVersion: v1\nkind: Config\nclusters: []\ncontexts: []\nusers: []\n")
	require.NoError(t, err)
	require.NoError(t, tmpKubeconfig.Close())
	app.Kubeconfig = tmpKubeconfig.Name()

	err = runDev(runDevParams{
		App:      app,
		Cmd:      cmd,
		NoTunnel: false,
		Server:   "http://dummy.local",
		Options:  []DevOption{WithEnvironmentDetector(detector)},
	})
	require.ErrorIs(t, err, ErrNoTunnelCredential)
	require.Contains(t, err.Error(), "--token")
}

func TestResolveDevTunnelEndpoint(t *testing.T) {
	assert.Equal(t, "diverge-tun-abc.dev.svc.cluster.local:3000", resolveDevTunnelEndpoint(tunnelclient.ReadyInfo{
		K8sServiceName:  "diverge-tun-abc",
		ProxyService:    "diverge-server-tunnel-proxy:8081",
		Endpoint:        "diverge-server-tunnel-proxy:8081",
		ProtocolVersion: 3,
	}, "feat-1", "dev", 3000))

	assert.Equal(t, "diverge-server-tunnel-proxy:8081", resolveDevTunnelEndpoint(tunnelclient.ReadyInfo{
		ProxyService:    "diverge-server-tunnel-proxy:8081",
		Endpoint:        "fallback:8081",
		ProtocolVersion: 3,
	}, "feat-1", "dev", 3000))

	assert.Equal(t, "custom-endpoint:8081", resolveDevTunnelEndpoint(tunnelclient.ReadyInfo{
		Endpoint:        "custom-endpoint:8081",
		ProtocolVersion: 3,
	}, "feat-1", "dev", 3000))

	assert.Equal(t, "diverge-tunnel-feat-1.dev.svc.cluster.local:8080", resolveDevTunnelEndpoint(tunnelclient.ReadyInfo{
		ProtocolVersion: 2,
	}, "feat-1", "dev", 8080))
}

type mockTunnelServerForbidden struct {
	divergev1alpha1connect.UnimplementedTunnelServiceHandler
}

func (m *mockTunnelServerForbidden) Tunnel(_ context.Context, _ *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
	return connect.NewError(connect.CodePermissionDenied, errors.New("namespace denied"))
}

func TestRunDev_TunnelFatalErrorFailsFast(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
		username:    "alice",
		gitBranch:   "main",
	}
	app, _, cmd, cancel := runDevTestSetup(t, detector)

	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(&mockTunnelServerForbidden{})
	mux.Handle(path, handler)
	ts := httptest.NewUnstartedServer(mux)
	ts.EnableHTTP2 = true
	ts.StartTLS()

	origTransport := http.DefaultTransport
	http.DefaultTransport = ts.Client().Transport
	t.Cleanup(func() {
		cancel()
		ts.Close()
		http.DefaultTransport = origTransport
	})

	start := time.Now()
	err := runDev(runDevParams{
		App:      app,
		Cmd:      cmd,
		NoTunnel: false,
		Server:   ts.URL,
		Token:    "test-token",
		Options:  []DevOption{WithEnvironmentDetector(detector)},
	})
	elapsed := time.Since(start)
	require.Error(t, err)
	require.Contains(t, err.Error(), "namespace denied")
	require.Less(t, elapsed, 2*time.Second, "fatal tunnel error should fail fast without waiting for 15s timeout")
}

type mockTunnelServerPostReadyClose struct {
	divergev1alpha1connect.UnimplementedTunnelServiceHandler
	triggerClose <-chan pb.CloseCode
}

func (m *mockTunnelServerPostReadyClose) Tunnel(ctx context.Context, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
	if _, err := stream.Receive(); err != nil {
		return err
	}
	if err := stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_Ready{
			Ready: &pb.TunnelReady{
				TunnelId:        "tun-ready-1",
				Endpoint:        "diverge-server-tunnel-proxy:8081",
				ProxyService:    "diverge-server-tunnel-proxy:8081",
				K8SServiceName:  "diverge-tunnel-ready",
				ProtocolVersion: 3,
			},
		},
	}); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return nil
	case code, ok := <-m.triggerClose:
		if !ok {
			return nil
		}
		return stream.Send(&pb.TunnelServiceTunnelResponse{
			Payload: &pb.TunnelServiceTunnelResponse_Close{
				Close: &pb.TunnelClose{
					Code:   code,
					Reason: "evicted by another session",
				},
			},
		})
	}
}

func TestRunDev_TunnelFatalErrorAfterReadyStopsSessionAndCleansUp(t *testing.T) {
	t.Run("fatal post-ready close terminates session, deletes PreviewGroup, and returns error", func(t *testing.T) {
		detector := fakeDetector{
			tailscaleIP: "100.100.100.100",
			serviceName: "web",
			username:    "alice",
			gitBranch:   "main",
		}
		app, c, cmd, cancel := runDevTestSetup(t, detector)
		defer cancel()

		triggerClose := make(chan pb.CloseCode, 1)
		srv := &mockTunnelServerPostReadyClose{triggerClose: triggerClose}
		mux := http.NewServeMux()
		path, handler := divergev1alpha1connect.NewTunnelServiceHandler(srv)
		mux.Handle(path, handler)
		ts := httptest.NewUnstartedServer(mux)
		ts.EnableHTTP2 = true
		ts.StartTLS()

		origTransport := http.DefaultTransport
		http.DefaultTransport = ts.Client().Transport

		errCh := make(chan error, 1)
		runDone := make(chan struct{})
		t.Cleanup(func() {
			cancel()
			close(triggerClose)
			select {
			case <-runDone:
			case <-time.After(3 * time.Second):
			}
			ts.Close()
			http.DefaultTransport = origTransport
		})

		go func() {
			defer close(runDone)
			errCh <- runDev(runDevParams{
				App:      app,
				Cmd:      cmd,
				NoTunnel: false,
				NoProxy:  true,
				Server:   ts.URL,
				Token:    "test-token",
				Options:  []DevOption{WithEnvironmentDetector(detector)},
			})
		}()

		var pg divergeiov1alpha1.PreviewGroup
		require.Eventually(t, func() bool {
			return c.Get(context.Background(), types.NamespacedName{Name: "dev-alice-web"}, &pg) == nil
		}, 3*time.Second, 20*time.Millisecond)

		triggerClose <- pb.CloseCode_CLOSE_CODE_EVICTED

		var runErr error
		select {
		case runErr = <-errCh:
		case <-time.After(3 * time.Second):
			t.Fatal("runDev did not terminate after post-ready fatal tunnel error")
		}
		require.Error(t, runErr)
		assert.Contains(t, runErr.Error(), "CLOSE_CODE_EVICTED")
		assert.Contains(t, runErr.Error(), "evicted by another session")

		err := c.Get(context.Background(), types.NamespacedName{Name: "dev-alice-web"}, &pg)
		assert.True(t, apierrors.IsNotFound(err), "PreviewGroup should be cleaned up on fatal post-ready tunnel error, got: %v", err)
	})

	t.Run("graceful parent cancel after ready returns nil and deletes PreviewGroup", func(t *testing.T) {
		detector := fakeDetector{
			tailscaleIP: "100.100.100.100",
			serviceName: "web",
			username:    "alice",
			gitBranch:   "main",
		}
		app, c, cmd, cancel := runDevTestSetup(t, detector)

		triggerClose := make(chan pb.CloseCode)
		srv := &mockTunnelServerPostReadyClose{triggerClose: triggerClose}
		mux := http.NewServeMux()
		path, handler := divergev1alpha1connect.NewTunnelServiceHandler(srv)
		mux.Handle(path, handler)
		ts := httptest.NewUnstartedServer(mux)
		ts.EnableHTTP2 = true
		ts.StartTLS()

		origTransport := http.DefaultTransport
		http.DefaultTransport = ts.Client().Transport

		errCh := make(chan error, 1)
		runDone := make(chan struct{})
		t.Cleanup(func() {
			cancel()
			close(triggerClose)
			select {
			case <-runDone:
			case <-time.After(3 * time.Second):
			}
			ts.Close()
			http.DefaultTransport = origTransport
		})

		go func() {
			defer close(runDone)
			errCh <- runDev(runDevParams{
				App:      app,
				Cmd:      cmd,
				NoTunnel: false,
				NoProxy:  true,
				Server:   ts.URL,
				Token:    "test-token",
				Options:  []DevOption{WithEnvironmentDetector(detector)},
			})
		}()

		var pg divergeiov1alpha1.PreviewGroup
		require.Eventually(t, func() bool {
			return c.Get(context.Background(), types.NamespacedName{Name: "dev-alice-web"}, &pg) == nil
		}, 3*time.Second, 20*time.Millisecond)

		cancel()

		var runErr error
		select {
		case runErr = <-errCh:
		case <-time.After(3 * time.Second):
			t.Fatal("runDev did not exit on parent context cancel")
		}
		require.NoError(t, runErr)

		err := c.Get(context.Background(), types.NamespacedName{Name: "dev-alice-web"}, &pg)
		assert.True(t, apierrors.IsNotFound(err), "PreviewGroup should be cleaned up on graceful cancel, got: %v", err)
	})
}

type recordingSessionManager struct {
	devsession.Manager
	releases atomic.Int32
}

func (m *recordingSessionManager) Release(ctx context.Context, namespace, service, sessionID string) error {
	m.releases.Add(1)
	return m.Manager.Release(ctx, namespace, service, sessionID)
}

func TestRunDev_ReleasesLeaseOnEarlyTunnelFailure(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
		username:    "alice",
		gitBranch:   "main",
	}
	app, c, cmd, cancel := runDevTestSetup(t, detector)
	sessMgr := &recordingSessionManager{Manager: devsession.NewSessionManager(c)}

	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(&mockTunnelServerForbidden{})
	mux.Handle(path, handler)
	ts := httptest.NewUnstartedServer(mux)
	ts.EnableHTTP2 = true
	ts.StartTLS()

	origTransport := http.DefaultTransport
	http.DefaultTransport = ts.Client().Transport
	t.Cleanup(func() {
		cancel()
		ts.Close()
		http.DefaultTransport = origTransport
	})

	err := runDev(runDevParams{
		App:      app,
		Cmd:      cmd,
		NoTunnel: false,
		NoProxy:  true,
		Server:   ts.URL,
		Token:    "test-token",
		Options:  []DevOption{WithEnvironmentDetector(detector), WithSessionManager(sessMgr)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace denied")
	assert.Equal(t, int32(1), sessMgr.releases.Load(), "acquired dev session lease should be released on tunnel setup failure")
}

func TestRunDev_CleanServerCloseBeforePreviewGroupCreateReturnsNil(t *testing.T) {
	detector := fakeDetector{
		tailscaleIP: "100.100.100.100",
		serviceName: "web",
		username:    "alice",
		gitBranch:   "main",
	}

	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)

	triggerClose := make(chan pb.CloseCode, 1)
	createEntered := make(chan struct{}, 1)
	c := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*divergeiov1alpha1.PreviewGroup); ok {
				select {
				case createEntered <- struct{}{}:
				default:
				}
				<-ctx.Done()
				return ctx.Err()
			}
			return cl.Create(ctx, obj, opts...)
		},
	}).Build()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	app := &App{Client: c, Clientset: k8sfake.NewSimpleClientset()}

	srv := &mockTunnelServerPostReadyClose{triggerClose: triggerClose}
	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(srv)
	mux.Handle(path, handler)
	ts := httptest.NewUnstartedServer(mux)
	ts.EnableHTTP2 = true
	ts.StartTLS()

	origTransport := http.DefaultTransport
	http.DefaultTransport = ts.Client().Transport

	errCh := make(chan error, 1)
	runDone := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		close(triggerClose)
		select {
		case <-runDone:
		case <-time.After(3 * time.Second):
		}
		ts.Close()
		http.DefaultTransport = origTransport
	})

	go func() {
		defer close(runDone)
		errCh <- runDev(runDevParams{
			App:      app,
			Cmd:      cmd,
			NoTunnel: false,
			NoProxy:  true,
			Server:   ts.URL,
			Token:    "test-token",
			Options:  []DevOption{WithEnvironmentDetector(detector)},
		})
	}()

	select {
	case <-createEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for PreviewGroup Create")
	}

	triggerClose <- pb.CloseCode_CLOSE_CODE_UNSPECIFIED

	select {
	case runErr := <-errCh:
		require.NoError(t, runErr)
	case <-time.After(3 * time.Second):
		t.Fatal("runDev did not return after clean server tunnel close during PreviewGroup Create")
	}
}
