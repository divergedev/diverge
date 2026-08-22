package server

import (
	"context"
	divergev1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/internal/server/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"log/slog"
	clifake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type tunnelTestFixture struct {
	tm          *TunnelManager
	rpcServer   *httptest.Server
	proxyServer *httptest.Server
	client      divergev1alpha1connect.TunnelServiceClient
}

func setupTunnelTestFixture(t *testing.T) *tunnelTestFixture {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = divergev1alpha1.AddToScheme(scheme)
	ctrlClient := clifake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.Default()
	auditLogger := NewAuditLogger(logger)
	fakeK8s := fake.NewSimpleClientset()
	fakeK8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = true
		return true, sar, nil
	})
	tm := NewTunnelManager(ctrlClient, fakeK8s, logger, auditLogger)

	rpcMux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(tm)
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.ContextWithUserInfo(r.Context(), &auth.UserInfo{Username: "test"})
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
	rpcMux.Handle(path, authHandler)

	rpcServer := httptest.NewUnstartedServer(rpcMux)
	rpcServer.EnableHTTP2 = true
	rpcServer.StartTLS()

	proxyMux := http.NewServeMux()
	proxyMux.Handle("/", tm.NewTunnelProxyHandler())
	proxyServer := httptest.NewServer(proxyMux)

	t.Cleanup(func() {
		rpcServer.Close()
		proxyServer.Close()
	})

	client := divergev1alpha1connect.NewTunnelServiceClient(rpcServer.Client(), rpcServer.URL)
	return &tunnelTestFixture{tm, rpcServer, proxyServer, client}
}

func TestTunnelManager(t *testing.T) {
	fx := setupTunnelTestFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := fx.client.Tunnel(ctx)

	// 1. Register
	err := stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Register{
			Register: &pb.TunnelRegister{
				Service:         "my-svc",
				Namespace:       "default",
				Port:            8080,
				PreviewId:       "test-preview",
				ProtocolVersion: 2,
			},
		},
	})
	require.NoError(t, err)

	// 2. Ready
	msg, err := stream.Receive()
	require.NoError(t, err)
	require.NotNil(t, msg.GetReady())
	require.NotEmpty(t, msg.GetReady().TunnelId)

	assert.True(t, fx.tm.HasTunnel("test-preview"))

	// 3. Pong to heartbeat
	err = stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Pong{
			Pong: &pb.TunnelPong{},
		},
	})
	require.NoError(t, err)

	// 4. ForwardRequest via proxy
	go func() {
		for {
			msg, err := stream.Receive()
			if err != nil {
				return
			}
			if msg.GetHttpRequest() != nil {
				_ = stream.Send(&pb.TunnelServiceTunnelRequest{
					Payload: &pb.TunnelServiceTunnelRequest_HttpResponse{
						HttpResponse: &pb.TunnelHTTPResponse{
							RequestId:  msg.GetHttpRequest().RequestId,
							StatusCode: 200,
							Headers: []*pb.HeaderEntry{
								{Key: "X-Test", Values: []string{"hello"}},
							},
							Body: []byte("OK"),
						},
					},
				})
				return
			}
		}
	}()

	req, _ := http.NewRequest("GET", fx.proxyServer.URL+"/api/users", nil)
	req.Header.Set("x-diverge-env", "test-preview")

	resp, err := fx.proxyServer.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)

	// 5. Cleanup
	_ = stream.CloseRequest()
	_ = stream.CloseResponse()
	cancel()
	time.Sleep(100 * time.Millisecond)
	assert.False(t, fx.tm.HasTunnel("test-preview"))
}

func TestTunnelManager_HasTunnel(t *testing.T) {
	fx := setupTunnelTestFixture(t)
	assert.False(t, fx.tm.HasTunnel("nonexistent"))
}

func TestExtractPreviewIDFromHost(t *testing.T) {
	tests := []struct {
		host     string
		expected string
	}{
		{"diverge-tunnel-my-preview.default.svc.cluster.local", "my-preview"},
		{"diverge-tunnel-my-preview.default.svc.cluster.local:8081", "my-preview"},
		{"diverge-tunnel-abc123.ns.svc", "abc123"},
		{"some-other-service.default.svc", ""},
		{"localhost:8080", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := extractPreviewIDFromHost(tt.host)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestHeadersToProto(t *testing.T) {
	h := http.Header{
		"Content-Type": {"application/json"},
		"Set-Cookie":   {"a=1", "b=2"},
	}

	entries := headersToProto(h)
	assert.Len(t, entries, 2)

	for _, e := range entries {
		if e.Key == "Set-Cookie" {
			assert.Equal(t, []string{"a=1", "b=2"}, e.Values)
		}
	}
}

func TestTunnelManager_NoRegisterMessage(t *testing.T) {
	fx := setupTunnelTestFixture(t)
	ctx := context.Background()
	stream := fx.client.Tunnel(ctx)

	err := stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Pong{
			Pong: &pb.TunnelPong{},
		},
	})
	require.NoError(t, err)

	_, err = stream.Receive()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "first message must be TunnelRegister")
}

func TestTunnelProxyHandler(t *testing.T) {
	tests := []struct {
		name           string
		previewHeader  string
		host           string
		body           string
		expectedStatus int
	}{
		{"no tunnel exists", "nonexistent", "", "", http.StatusBadGateway},
		{"bad host no header", "", "unknown.host.local", "", http.StatusBadRequest},
		{"body too large", "test-large", "", strings.Repeat("x", (1<<20)+10), http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := setupTunnelTestFixture(t)

			// Setup stream for body too large to pass HasTunnel check
			if tt.name == "body too large" {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				stream := fx.client.Tunnel(ctx)
				err := stream.Send(&pb.TunnelServiceTunnelRequest{
					Payload: &pb.TunnelServiceTunnelRequest_Register{
						Register: &pb.TunnelRegister{
							Service:         "my-svc",
							Namespace:       "default",
							Port:            8080,
							PreviewId:       "test-large",
							ProtocolVersion: 2,
						},
					},
				})
				require.NoError(t, err)
				_, err = stream.Receive()
				require.NoError(t, err)
			}

			var req *http.Request
			var err error
			if tt.body != "" {
				req, err = http.NewRequest("POST", fx.proxyServer.URL+"/", strings.NewReader(tt.body))
			} else {
				req, err = http.NewRequest("GET", fx.proxyServer.URL+"/", nil)
			}
			require.NoError(t, err)

			if tt.previewHeader != "" {
				req.Header.Set("x-diverge-env", tt.previewHeader)
			}
			if tt.host != "" {
				req.Host = tt.host
			}

			resp, err := fx.proxyServer.Client().Do(req)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
		})
	}
}

func TestTunnelManager_ForwardRequestTimeout(t *testing.T) {
	fx := setupTunnelTestFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := fx.client.Tunnel(ctx)
	err := stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Register{
			Register: &pb.TunnelRegister{
				Service:         "my-svc",
				Namespace:       "default",
				Port:            8080,
				PreviewId:       "test-timeout",
				ProtocolVersion: 2,
			},
		},
	})
	require.NoError(t, err)
	_, err = stream.Receive() // Ready
	require.NoError(t, err)

	fwdCtx, fwdCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer fwdCancel()

	_, err = fx.tm.ForwardRequest(fwdCtx, "test-timeout", &pb.TunnelHTTPRequest{RequestId: "req-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

func TestTunnelManager_ChunkedResponse(t *testing.T) {
	fx := setupTunnelTestFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := fx.client.Tunnel(ctx)
	err := stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Register{
			Register: &pb.TunnelRegister{
				Service:         "my-svc",
				Namespace:       "default",
				Port:            8080,
				PreviewId:       "test-chunked",
				ProtocolVersion: 2,
			},
		},
	})
	require.NoError(t, err)

	_, err = stream.Receive() // Ready
	require.NoError(t, err)

	go func() {
		msg, err := stream.Receive()
		if err != nil {
			return
		}
		if req := msg.GetHttpRequest(); req != nil {
			_ = stream.Send(&pb.TunnelServiceTunnelRequest{
				Payload: &pb.TunnelServiceTunnelRequest_HttpResponse{
					HttpResponse: &pb.TunnelHTTPResponse{
						RequestId:  req.RequestId,
						StatusCode: 201,
						Headers: []*pb.HeaderEntry{
							{Key: "X-Chunked", Values: []string{"true"}},
						},
						Body:          []byte("chunk1"),
						HasMoreChunks: true,
					},
				},
			})
			_ = stream.Send(&pb.TunnelServiceTunnelRequest{
				Payload: &pb.TunnelServiceTunnelRequest_ResponseChunk{
					ResponseChunk: &pb.TunnelResponseChunk{
						RequestId: req.RequestId,
						Data:      []byte("-chunk2"),
						IsLast:    false,
					},
				},
			})
			_ = stream.Send(&pb.TunnelServiceTunnelRequest{
				Payload: &pb.TunnelServiceTunnelRequest_ResponseChunk{
					ResponseChunk: &pb.TunnelResponseChunk{
						RequestId: req.RequestId,
						Data:      []byte("-chunk3"),
						IsLast:    true,
					},
				},
			})
		}
	}()

	resp, err := fx.tm.ForwardRequest(context.Background(), "test-chunked", &pb.TunnelHTTPRequest{RequestId: "req-1"})
	require.NoError(t, err)

	assert.Equal(t, int32(201), resp.StatusCode)
	assert.Equal(t, "chunk1-chunk2-chunk3", string(resp.Body))
	assert.Len(t, resp.Headers, 1)
	assert.Equal(t, "X-Chunked", resp.Headers[0].Key)
}

func TestTunnelManager_NonBlockingSend(t *testing.T) {
	fx := setupTunnelTestFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := fx.client.Tunnel(ctx)
	_ = stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Register{
			Register: &pb.TunnelRegister{
				Service:         "my-svc",
				Namespace:       "default",
				Port:            8080,
				PreviewId:       "test-nonblock",
				ProtocolVersion: 2,
			},
		},
	})
	_, _ = stream.Receive() // Ready

	go func() {
		_, _ = stream.Receive()

		_ = stream.Send(&pb.TunnelServiceTunnelRequest{
			Payload: &pb.TunnelServiceTunnelRequest_HttpResponse{
				HttpResponse: &pb.TunnelHTTPResponse{
					RequestId:  "req-1",
					StatusCode: 200,
					Body:       []byte("resp1"),
				},
			},
		})
		_ = stream.Send(&pb.TunnelServiceTunnelRequest{
			Payload: &pb.TunnelServiceTunnelRequest_HttpResponse{
				HttpResponse: &pb.TunnelHTTPResponse{
					RequestId:  "req-1",
					StatusCode: 200,
					Body:       []byte("resp2"),
				},
			},
		})
	}()

	resp, err := fx.tm.ForwardRequest(context.Background(), "test-nonblock", &pb.TunnelHTTPRequest{RequestId: "req-1"})
	require.NoError(t, err)
	assert.Equal(t, "resp1", string(resp.Body))
}
