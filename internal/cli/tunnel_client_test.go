package cli

import (
	"context"
	"fmt"
	"google.golang.org/protobuf/types/known/timestamppb"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	tc := NewTunnelClient(ts.URL, port, "test-preview", "test-service", "default",
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

	tc := NewTunnelClient(ts.URL, port, "test-preview", "test-service", "default", slog.Default())
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

	tc := NewTunnelClient(ts.URL, port, "test-preview", "test-service", "default", slog.Default())
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
