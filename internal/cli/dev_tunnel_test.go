package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
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
