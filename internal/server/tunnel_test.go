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

func TestTunnelManager(t *testing.T) {
	scheme := runtime.NewScheme()
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

	// RPC server
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
	defer rpcServer.Close()

	// Proxy server (separate, no auth — like production)
	proxyMux := http.NewServeMux()
	proxyMux.Handle("/", tm.NewTunnelProxyHandler())
	proxyServer := httptest.NewServer(proxyMux)
	defer proxyServer.Close()

	client := divergev1alpha1connect.NewTunnelServiceClient(rpcServer.Client(), rpcServer.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := client.Tunnel(ctx)

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
	// tunnel_id is now a UUID, not the preview-id
	require.NotEmpty(t, msg.GetReady().TunnelId)

	assert.True(t, tm.HasTunnel("test-preview"))

	// 3. Pong to heartbeat (send a pong so server doesn't time us out)
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

	req, _ := http.NewRequest("GET", proxyServer.URL+"/api/users", nil)
	req.Header.Set("x-diverge-env", "test-preview")

	resp, err := proxyServer.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)

	// 5. Cleanup
	_ = stream.CloseRequest()
	_ = stream.CloseResponse()
	cancel()
	time.Sleep(100 * time.Millisecond)
	assert.False(t, tm.HasTunnel("test-preview"))
}

func TestTunnelManager_HasTunnel(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = divergev1alpha1.AddToScheme(scheme)
	ctrlClient := clifake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.Default()
	auditLogger := NewAuditLogger(logger)
	fakeK8s := fake.NewSimpleClientset()

	tm := NewTunnelManager(ctrlClient, fakeK8s, logger, auditLogger)
	assert.False(t, tm.HasTunnel("nonexistent"))
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

	// Find Set-Cookie entry and verify multi-values preserved
	for _, e := range entries {
		if e.Key == "Set-Cookie" {
			assert.Equal(t, []string{"a=1", "b=2"}, e.Values)
		}
	}
}

func TestTunnelManager_NoRegisterMessage(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = divergev1alpha1.AddToScheme(scheme) // If this exists
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
	defer rpcServer.Close()

	client := divergev1alpha1connect.NewTunnelServiceClient(rpcServer.Client(), rpcServer.URL)
	ctx := context.Background()
	stream := client.Tunnel(ctx)

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

func TestTunnelManager_ProxyNoTunnel(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = divergev1alpha1.AddToScheme(scheme) // If this exists
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

	proxyMux := http.NewServeMux()
	proxyMux.Handle("/", tm.NewTunnelProxyHandler())
	proxyServer := httptest.NewServer(proxyMux)
	defer proxyServer.Close()

	req, _ := http.NewRequest("GET", proxyServer.URL+"/", nil)
	req.Header.Set("x-diverge-env", "nonexistent")

	resp, err := proxyServer.Client().Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

func TestTunnelManager_ProxyBadHost(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = divergev1alpha1.AddToScheme(scheme) // If this exists
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

	proxyMux := http.NewServeMux()
	proxyMux.Handle("/", tm.NewTunnelProxyHandler())
	proxyServer := httptest.NewServer(proxyMux)
	defer proxyServer.Close()

	req, _ := http.NewRequest("GET", proxyServer.URL+"/", nil)
	req.Host = "unknown.host.local"

	resp, err := proxyServer.Client().Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestTunnelManager_ProxyBodyTooLarge(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = divergev1alpha1.AddToScheme(scheme) // If this exists
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

	proxyMux := http.NewServeMux()
	proxyMux.Handle("/", tm.NewTunnelProxyHandler())
	proxyServer := httptest.NewServer(proxyMux)
	defer proxyServer.Close()

	// 1MB + 10 bytes
	body := strings.Repeat("a", (1<<20)+10)
	req, _ := http.NewRequest("POST", proxyServer.URL+"/", strings.NewReader(body))
	req.Header.Set("x-diverge-env", "nonexistent") // wait, if it validates env first, it will fail 502. So we need to mock HasTunnel.

	// Since we cant easily mock HasTunnel, we just register a real tunnel.
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
	defer rpcServer.Close()

	client := divergev1alpha1connect.NewTunnelServiceClient(rpcServer.Client(), rpcServer.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := client.Tunnel(ctx)
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

	_, err = stream.Receive() // Ready
	require.NoError(t, err)

	req.Header.Set("x-diverge-env", "test-large")
	resp, err := proxyServer.Client().Do(req)
	require.NoError(t, err)
	// Currently returns 413
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestTunnelManager_ForwardRequestTimeout(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = divergev1alpha1.AddToScheme(scheme) // If this exists
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
	defer rpcServer.Close()

	client := divergev1alpha1connect.NewTunnelServiceClient(rpcServer.Client(), rpcServer.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := client.Tunnel(ctx)
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

	// ForwardRequest should timeout because client doesnt reply
	_, err = tm.ForwardRequest(fwdCtx, "test-timeout", &pb.TunnelHTTPRequest{RequestId: "req-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

func TestTunnelManager_ChunkedResponse(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = divergev1alpha1.AddToScheme(scheme) // If this exists
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
	defer rpcServer.Close()

	client := divergev1alpha1connect.NewTunnelServiceClient(rpcServer.Client(), rpcServer.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := client.Tunnel(ctx)
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

	// In client goroutine: receive the request, send a response with HasMoreChunks: true, then send ResponseChunk messages, final one with IsLast: true
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

	resp, err := tm.ForwardRequest(context.Background(), "test-chunked", &pb.TunnelHTTPRequest{RequestId: "req-1"})
	require.NoError(t, err)

	assert.Equal(t, int32(201), resp.StatusCode)
	assert.Equal(t, "chunk1-chunk2-chunk3", string(resp.Body))
	assert.Len(t, resp.Headers, 1)
	assert.Equal(t, "X-Chunked", resp.Headers[0].Key)
}

func TestTunnelManager_NonBlockingSend(t *testing.T) {
	// We want to test that the receive goroutine doesnt block when a respCh is full (capacity 1) and a duplicate request_id arrives.
	// We can do this by sending two HttpResponse for the same RequestId without waiting for the first to be consumed.
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = divergev1alpha1.AddToScheme(scheme) // If this exists
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
	defer rpcServer.Close()

	client := divergev1alpha1connect.NewTunnelServiceClient(rpcServer.Client(), rpcServer.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := client.Tunnel(ctx)
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
		// Wait for ForwardRequest to be called and send the request
		_, _ = stream.Receive()

		// Send two responses for the same request
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

	resp, err := tm.ForwardRequest(context.Background(), "test-nonblock", &pb.TunnelHTTPRequest{RequestId: "req-1"})
	require.NoError(t, err)
	// We expect the first response to have been received successfully, and the second one dropped (non-blocking)
	assert.Equal(t, "resp1", string(resp.Body))
}
