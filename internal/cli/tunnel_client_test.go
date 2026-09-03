package cli

import (
	"context"
	"fmt"
	"google.golang.org/protobuf/types/known/timestamppb"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	"pgregory.net/rapid"
)

type mockTunnelServer struct {
	divergev1alpha1connect.UnimplementedTunnelServiceHandler
	msgCh chan *pb.TunnelServiceTunnelRequest
}

func (m *mockTunnelServer) Tunnel(ctx context.Context, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
	// First message should be Register
	msg, err := stream.Receive()
	if err != nil {
		return fmt.Errorf("failed to receive register: %w", err)
	}
	if msg.GetRegister() == nil {
		return fmt.Errorf("expected register message, got %T", msg.Payload)
	}
	m.msgCh <- msg

	// Send Ready
	_ = stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_Ready{
			Ready: &pb.TunnelReady{
				TunnelId: "tunnel-123",
				Endpoint: "http://test-preview.diverge.local",
			},
		},
	})

	// Wait for further messages (e.g. Pong, Response)
	go func() {
		for {
			msg, err := stream.Receive()
			if err != nil {
				return
			}
			m.msgCh <- msg
		}
	}()

	// Simulate sending a Ping
	_ = stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_Ping{
			Ping: &pb.TunnelPing{
				Timestamp: timestamppb.Now(),
			},
		},
	})

	// Simulate sending a Request
	_ = stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_HttpRequest{
			HttpRequest: &pb.TunnelHTTPRequest{
				RequestId: "req-1",
				Method:    "GET",
				Path:      "/foo",
			},
		},
	})

	<-ctx.Done()
	return nil
}

func TestTunnelClient(t *testing.T) {
	// Setup a local mock application server
	appServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/foo", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("mock app response"))
	}))
	defer appServer.Close()

	// Extract port
	var port int
	_, _ = fmt.Sscanf(appServer.Listener.Addr().String(), "127.0.0.1:%d", &port)

	// Setup a mock tunnel server
	msgCh := make(chan *pb.TunnelServiceTunnelRequest, 10)
	mockServer := &mockTunnelServer{msgCh: msgCh}
	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(mockServer)
	mux.Handle(path, handler)

	ts := httptest.NewUnstartedServer(mux)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	tc := NewTunnelClient(ts.URL, port, "test-preview", "test-service", "default", "test-token",
		slog.New(slog.NewTextHandler(os.Stdout, nil)))
	tc.httpClient = ts.Client() // use test server's TLS client
	tc.tunnelHTTPClient = ts.Client()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run client
	go tc.ConnectWithRetry(ctx)

	// Wait for Register
	var regMsg *pb.TunnelServiceTunnelRequest
	select {
	case regMsg = <-msgCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for register")
	}
	assert.Equal(t, "test-preview", regMsg.GetRegister().PreviewId)
	assert.Equal(t, int32(2), regMsg.GetRegister().ProtocolVersion)

	// Wait for Pong
	var pongMsg *pb.TunnelServiceTunnelRequest
	select {
	case pongMsg = <-msgCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for pong")
	}
	assert.NotNil(t, pongMsg.GetPong())

	// Wait for Response
	var respMsg *pb.TunnelServiceTunnelRequest
	select {
	case respMsg = <-msgCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for response")
	}
	require.NotNil(t, respMsg.GetHttpResponse())
	assert.Equal(t, "req-1", respMsg.GetHttpResponse().RequestId)
	assert.Equal(t, int32(200), respMsg.GetHttpResponse().StatusCode)
	assert.Equal(t, "mock app response", string(respMsg.GetHttpResponse().Body))

	// Clean up
	cancel()
	time.Sleep(200 * time.Millisecond) // Let it exit gracefully
}

func TestTunnelClient_ReadyChannel(t *testing.T) {
	appServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer appServer.Close()
	var port int
	_, _ = fmt.Sscanf(appServer.Listener.Addr().String(), "127.0.0.1:%d", &port)

	msgCh := make(chan *pb.TunnelServiceTunnelRequest, 10)
	mockServer := &mockTunnelServer{msgCh: msgCh}
	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(mockServer)
	mux.Handle(path, handler)
	ts := httptest.NewUnstartedServer(mux)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	tc := NewTunnelClient(ts.URL, port, "test-preview", "test-service", "default", "test-token", slog.Default())
	tc.httpClient = ts.Client()
	tc.tunnelHTTPClient = ts.Client()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tc.ConnectWithRetry(ctx)

	select {
	case <-tc.Ready:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Ready channel was not closed")
	}
}

type mockReconnectServer struct {
	divergev1alpha1connect.UnimplementedTunnelServiceHandler
	msgCh chan *pb.TunnelServiceTunnelRequest
	t     *testing.T
	count int
}

func (m *mockReconnectServer) Tunnel(ctx context.Context, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
	m.count++
	msg, err := stream.Receive()
	if err != nil {
		return err
	}
	m.msgCh <- msg

	if m.count == 1 {
		// First connection, simulate server close
		return fmt.Errorf("server closed stream")
	}

	// Second connection, send ready and wait
	_ = stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_Ready{
			Ready: &pb.TunnelReady{
				TunnelId: "tunnel-456",
				Endpoint: "http://test-preview.diverge.local",
			},
		},
	})
	<-ctx.Done()
	return nil
}

func TestTunnelClient_Reconnect(t *testing.T) {
	appServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer appServer.Close()
	var port int
	_, _ = fmt.Sscanf(appServer.Listener.Addr().String(), "127.0.0.1:%d", &port)

	msgCh := make(chan *pb.TunnelServiceTunnelRequest, 10)
	mockServer := &mockReconnectServer{msgCh: msgCh, t: t}
	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(mockServer)
	mux.Handle(path, handler)
	ts := httptest.NewUnstartedServer(mux)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	tc := NewTunnelClient(ts.URL, port, "test-preview", "test-service", "default", "test-token", slog.Default())
	tc.httpClient = ts.Client()
	tc.tunnelHTTPClient = ts.Client()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tc.ConnectWithRetry(ctx)

	// Wait for first Register
	select {
	case <-msgCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for first register")
	}

	// Wait for second Register
	select {
	case <-msgCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for second register")
	}
}

// TestNewTunnelClient_SendsAuthorizationHeader is the regression guard for the
// tunnel connecting with no credential at all. The server authenticates every
// Tunnel RPC by TokenReview and rejects an unauthenticated request with 401,
// so the header has to reach the wire.
func TestNewTunnelClient_SendsAuthorizationHeader(t *testing.T) {
	var gotAuth string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tc := NewTunnelClient(srv.URL, 8080, "preview-1", "svc", "ns", "s3cret-token", slog.Default())

	req, err := http.NewRequest(http.MethodPost, srv.URL, nil)
	require.NoError(t, err)
	resp, err := tc.tunnelHTTPClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "Bearer s3cret-token", gotAuth)
}

// TestTunnelAuthTransport_DoesNotMutateRequest pins the RoundTripper contract:
// the caller's request must not be modified in place.
func TestTunnelAuthTransport_DoesNotMutateRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	transport := &tunnelAuthTransport{base: http.DefaultTransport, token: "tok"}
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Empty(t, req.Header.Get("Authorization"),
		"RoundTrip must not modify the request it is given")
}

func TestResolveTunnelToken(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("  file-token\n"), 0o600))

	tests := []struct {
		name     string
		explicit string
		env      string
		restCfg  *rest.Config
		want     string
		wantErr  error
	}{
		{
			name:     "explicit token wins",
			explicit: "flag-token",
			env:      "env-token",
			restCfg:  &rest.Config{BearerToken: "kube-token"},
			want:     "flag-token",
		},
		{
			name:    "env var used when no flag",
			env:     "env-token",
			restCfg: &rest.Config{BearerToken: "kube-token"},
			want:    "env-token",
		},
		{
			name:    "kubeconfig bearer token is the fallback",
			restCfg: &rest.Config{BearerToken: "kube-token"},
			want:    "kube-token",
		},
		{
			name:    "bearer token file is read",
			restCfg: &rest.Config{BearerTokenFile: tokenFile},
			want:    "file-token",
		},
		{
			name:     "surrounding whitespace is trimmed",
			explicit: "  spaced  ",
			want:     "spaced",
		},
		{
			name:    "no credential anywhere",
			restCfg: &rest.Config{},
			wantErr: ErrNoTunnelCredential,
		},
		{
			name:    "nil rest config",
			wantErr: ErrNoTunnelCredential,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tunnelTokenEnvVar, tt.env)
			got, err := resolveTunnelToken(tt.explicit, tt.restCfg)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveTunnelToken_PBT(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		tokenGen := rapid.StringMatching(`[a-zA-Z0-9_\-\.]{1,40}`)
		wsGen := rapid.StringMatching(`[ \t\r\n]{0,4}`)

		// Property 1: Explicit non-whitespace token always wins
		cleanExplicit := tokenGen.Draw(rt, "cleanExplicit")
		explicit := wsGen.Draw(rt, "wsPre1") + cleanExplicit + wsGen.Draw(rt, "wsPost1")
		envToken := wsGen.Draw(rt, "wsPre2") + tokenGen.Draw(rt, "cleanEnv") + wsGen.Draw(rt, "wsPost2")
		kubeToken := wsGen.Draw(rt, "wsPre3") + tokenGen.Draw(rt, "cleanKube") + wsGen.Draw(rt, "wsPost3")

		t.Setenv(tunnelTokenEnvVar, envToken)
		restCfg := &rest.Config{BearerToken: kubeToken}

		got, err := resolveTunnelToken(explicit, restCfg)
		require.NoError(t, err)
		assert.Equal(t, cleanExplicit, got, "explicit token must win over env and kubeconfig")
		assert.Equal(t, strings.TrimSpace(got), got, "token must never have surrounding whitespace")

		// Property 2: When explicit is whitespace-only, env token wins over kubeconfig
		onlyWS := wsGen.Draw(rt, "onlyWS")
		got, err = resolveTunnelToken(onlyWS, restCfg)
		require.NoError(t, err)
		assert.Equal(t, strings.TrimSpace(envToken), got, "env token must win when explicit token is whitespace-only")
		assert.Equal(t, strings.TrimSpace(got), got)

		// Property 3: When explicit and env are empty/whitespace, kubeconfig wins
		t.Setenv(tunnelTokenEnvVar, onlyWS)
		got, err = resolveTunnelToken(onlyWS, restCfg)
		require.NoError(t, err)
		assert.Equal(t, strings.TrimSpace(kubeToken), got, "kubeconfig token must win when flag and env are absent")
		assert.Equal(t, strings.TrimSpace(got), got)

		// Property 4: When all sources lack a credential, ErrNoTunnelCredential is returned
		emptyRestCfg := &rest.Config{BearerToken: onlyWS}
		_, err = resolveTunnelToken(onlyWS, emptyRestCfg)
		assert.ErrorIs(t, err, ErrNoTunnelCredential)

		_, err = resolveTunnelToken(onlyWS, nil)
		assert.ErrorIs(t, err, ErrNoTunnelCredential)
	})
}
