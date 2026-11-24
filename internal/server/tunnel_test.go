package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
	clifake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	divergev1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/server/auth"
)

type tunnelTestFixture struct {
	tm          *TunnelManager
	rpcServer   *httptest.Server
	proxyServer *httptest.Server
	client      divergev1alpha1connect.TunnelServiceClient
	fakeK8s     *fake.Clientset
}

func setupTunnelTestFixture(t *testing.T) *tunnelTestFixture {
	return setupTunnelTestFixtureWithConfig(t, nil, nil, nil)
}

func setupTunnelTestFixtureWithConfig(
	t *testing.T,
	tmCfgFn func(*TunnelManagerConfig),
	sarFn func(*authorizationv1.SubjectAccessReview),
	userFn func(*http.Request) *auth.UserInfo,
) *tunnelTestFixture {
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
		if sarFn != nil {
			sarFn(sar)
		} else {
			sar.Status.Allowed = true
		}
		return true, sar, nil
	})
	fakeK8s.PrependReactor("create", "leases", func(action k8stesting.Action) (bool, runtime.Object, error) {
		lease := action.(k8stesting.CreateAction).GetObject().(*coordinationv1.Lease)
		if lease.UID == "" {
			lease.UID = types.UID("lease-uid-" + lease.Name)
		}
		if lease.ResourceVersion == "" {
			lease.ResourceVersion = "1"
		}
		return false, nil, nil
	})

	cfg := TunnelManagerConfig{
		CRDClient:   ctrlClient,
		K8sClient:   fakeK8s,
		Logger:      logger,
		AuditLogger: auditLogger,
	}
	if tmCfgFn != nil {
		tmCfgFn(&cfg)
	}
	tm := NewTunnelManagerWithConfig(cfg)

	rpcMux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(tm)
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := &auth.UserInfo{Username: "test"}
		if userFn != nil {
			user = userFn(r)
		}
		ctx := r.Context()
		if user != nil {
			ctx = auth.ContextWithUserInfo(ctx, user)
		}
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
	rpcMux.Handle(path, authHandler)

	rpcServer := httptest.NewUnstartedServer(rpcMux)
	rpcServer.EnableHTTP2 = true
	rpcServer.StartTLS()

	proxyMux := http.NewServeMux()
	proxyMux.Handle("/", tm.NewTunnelProxyHandler())
	proxyServer := httptest.NewUnstartedServer(proxyMux)
	proxyServer.EnableHTTP2 = true
	proxyServer.StartTLS()

	t.Cleanup(func() {
		rpcServer.CloseClientConnections()
		proxyServer.CloseClientConnections()
		rpcServer.Close()
		proxyServer.Close()
	})

	client := divergev1alpha1connect.NewTunnelServiceClient(rpcServer.Client(), rpcServer.URL)
	return &tunnelTestFixture{
		tm:          tm,
		rpcServer:   rpcServer,
		proxyServer: proxyServer,
		client:      client,
		fakeK8s:     fakeK8s,
	}
}

type testClientPacketStream struct {
	stream *connect.BidiStreamForClient[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]
}

func (s *testClientPacketStream) RecvPacket() ([]byte, *pb.TunnelClose, error) {
	for {
		msg, err := s.stream.Receive()
		if err != nil {
			return nil, nil, err
		}
		switch p := msg.Payload.(type) {
		case *pb.TunnelServiceTunnelResponse_RawData:
			if len(p.RawData) > 0 {
				return p.RawData, nil, nil
			}
		case *pb.TunnelServiceTunnelResponse_Close:
			return nil, p.Close, nil
		}
	}
}

func (s *testClientPacketStream) SendRawData(data []byte) error {
	return s.stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_RawData{RawData: data},
	})
}

func (s *testClientPacketStream) SendClose(closeMsg *pb.TunnelClose) error {
	return s.stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Close{Close: closeMsg},
	})
}

func dialV3TestTunnel(
	t *testing.T,
	ctx context.Context,
	client divergev1alpha1connect.TunnelServiceClient,
	reg *pb.TunnelRegister,
	localHandler http.Handler,
) (*pb.TunnelReady, *bidiStreamConn, error) {
	t.Helper()
	stream := client.Tunnel(ctx)
	if err := stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Register{Register: reg},
	}); err != nil {
		return nil, nil, err
	}
	msg, err := stream.Receive()
	if err != nil {
		return nil, nil, err
	}
	ready := msg.GetReady()
	if ready == nil {
		return nil, nil, fmt.Errorf("expected TunnelReady, got %T", msg.Payload)
	}

	conn := newBidiStreamConn(&testClientPacketStream{stream: stream})
	conn.BeginDrain()
	go serveTunnelHTTP2(conn, localHandler)
	var closeOnce sync.Once
	closeClient := func() {
		closeOnce.Do(func() {
			conn.CloseStream()
			conn.WaitWriteDone(time.Second)
			_ = stream.CloseRequest()
			_ = stream.CloseResponse()
		})
	}
	go func() {
		<-conn.Done()
		closeClient()
	}()
	t.Cleanup(closeClient)
	return ready, conn, nil
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
	assert.Eventually(t, func() bool {
		return !fx.tm.HasTunnel("test-preview")
	}, time.Second, 10*time.Millisecond)
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

func TestExtractPreviewAndNamespaceFromHost(t *testing.T) {
	tests := []struct {
		host          string
		wantPreviewID string
		wantNamespace string
	}{
		{"diverge-tunnel-my-preview.default.svc.cluster.local", "my-preview", "default"},
		{"diverge-tunnel-my-preview.ns-a.svc.cluster.local:8081", "my-preview", "ns-a"},
		{"diverge-tunnel-abc123.ns-b.svc", "abc123", "ns-b"},
		{"diverge-tunnel-bare:8081", "bare", ""},
		{"diverge-tunnel-.default.svc", "", ""},
		{"some-other-service.default.svc", "", ""},
		{"localhost:8080", "", ""},
		{"", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			gotPreview, gotNS := extractPreviewAndNamespaceFromHost(tt.host)
			assert.Equal(t, tt.wantPreviewID, gotPreview)
			assert.Equal(t, tt.wantNamespace, gotNS)
		})
	}

	svcName, previewID, ns := extractServicePreviewAndNamespaceFromHost("diverge-tsvc-my-preview-api-1234abcd.ns-c.svc.cluster.local:8081")
	assert.Equal(t, "diverge-tsvc-my-preview-api-1234abcd", svcName)
	assert.Empty(t, previewID)
	assert.Equal(t, "ns-c", ns)

	emptySvc, emptyPrev, emptyNS := extractServicePreviewAndNamespaceFromHost("diverge-tsvc-.ns-c.svc")
	assert.Empty(t, emptySvc)
	assert.Empty(t, emptyPrev)
	assert.Empty(t, emptyNS)
}

func TestTunnelManager_CrossNamespaceIsolation(t *testing.T) {
	fx := setupTunnelTestFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Register tunnel with preview-id "shared-preview" in "namespace-a"
	streamA := fx.client.Tunnel(ctx)
	require.NoError(t, streamA.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Register{
			Register: &pb.TunnelRegister{
				Service:         "svc-a",
				Namespace:       "namespace-a",
				Port:            8080,
				PreviewId:       "shared-preview",
				ProtocolVersion: 2,
			},
		},
	}))
	msgA, err := streamA.Receive()
	require.NoError(t, err)
	require.NotNil(t, msgA.GetReady())

	// Register tunnel with the SAME preview-id "shared-preview" in "namespace-b"
	streamB := fx.client.Tunnel(ctx)
	require.NoError(t, streamB.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Register{
			Register: &pb.TunnelRegister{
				Service:         "svc-b",
				Namespace:       "namespace-b",
				Port:            8080,
				PreviewId:       "shared-preview",
				ProtocolVersion: 2,
			},
		},
	}))
	msgB, err := streamB.Receive()
	require.NoError(t, err)
	require.NotNil(t, msgB.GetReady())

	// Both namespaces must have active tunnels simultaneously
	assert.True(t, fx.tm.HasTunnelInNamespace("namespace-a", "shared-preview"))
	assert.True(t, fx.tm.HasTunnelInNamespace("namespace-b", "shared-preview"))
	// Unqualified lookup is ambiguous when both namespaces have active tunnels with the same preview-id
	assert.False(t, fx.tm.HasTunnel("shared-preview"))

	// Respond distinctly on streamA and streamB
	go func() {
		for {
			m, err := streamA.Receive()
			if err != nil {
				return
			}
			if req := m.GetHttpRequest(); req != nil {
				_ = streamA.Send(&pb.TunnelServiceTunnelRequest{
					Payload: &pb.TunnelServiceTunnelRequest_HttpResponse{
						HttpResponse: &pb.TunnelHTTPResponse{
							RequestId:  req.RequestId,
							StatusCode: 200,
							Body:       []byte("from-namespace-a"),
						},
					},
				})
				return
			}
		}
	}()
	go func() {
		for {
			m, err := streamB.Receive()
			if err != nil {
				return
			}
			if req := m.GetHttpRequest(); req != nil {
				_ = streamB.Send(&pb.TunnelServiceTunnelRequest{
					Payload: &pb.TunnelServiceTunnelRequest_HttpResponse{
						HttpResponse: &pb.TunnelHTTPResponse{
							RequestId:  req.RequestId,
							StatusCode: 200,
							Body:       []byte("from-namespace-b"),
						},
					},
				})
				return
			}
		}
	}()

	// 1. Ambiguous bare Host without namespace returns 502 Bad Gateway
	ambiguousReq, err := http.NewRequest("GET", fx.proxyServer.URL+"/hello", nil)
	require.NoError(t, err)
	ambiguousReq.Host = "diverge-tunnel-shared-preview:8081"
	ambiguousResp, err := fx.proxyServer.Client().Do(ambiguousReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadGateway, ambiguousResp.StatusCode)

	// 2. Host with namespace-a routes strictly to namespace-a's tunnel
	reqA, err := http.NewRequest("GET", fx.proxyServer.URL+"/hello", nil)
	require.NoError(t, err)
	reqA.Host = "diverge-tunnel-shared-preview.namespace-a.svc.cluster.local:8081"
	respA, err := fx.proxyServer.Client().Do(reqA)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respA.StatusCode)
	bodyA, _ := io.ReadAll(respA.Body)
	_ = respA.Body.Close()
	assert.Equal(t, "from-namespace-a", string(bodyA))

	// 3. Host with namespace-b routes strictly to namespace-b's tunnel
	reqB, err := http.NewRequest("GET", fx.proxyServer.URL+"/hello", nil)
	require.NoError(t, err)
	reqB.Host = "diverge-tunnel-shared-preview.namespace-b.svc.cluster.local:8081"
	respB, err := fx.proxyServer.Client().Do(reqB)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respB.StatusCode)
	bodyB, _ := io.ReadAll(respB.Body)
	_ = respB.Body.Close()
	assert.Equal(t, "from-namespace-b", string(bodyB))
}

func TestTunnelManager_RegistrationValidationAndOwnership(t *testing.T) {
	fx := setupTunnelTestFixtureWithConfig(t, func(cfg *TunnelManagerConfig) {
		cfg.SharedPreviewOwners = []string{"gitlab-ci"}
	}, nil, nil)
	ctx := context.Background()

	// 1. Invalid DNS-1123 preview_id rejected
	stream := fx.client.Tunnel(ctx)
	require.NoError(t, stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Register{
			Register: &pb.TunnelRegister{
				PreviewId: "INVALID_PREVIEW_ID",
				Namespace: "default",
				Service:   "api",
				Port:      8080,
			},
		},
	}))
	_, err := stream.Receive()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "preview_id")

	// 2. Cluster-scoped PreviewGroup (Namespace: "") owned by alice blocks non-owners,
	//    and Environment in another namespace does not block "default".
	clusterPG := &divergev1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pg-owned-cluster",
		},
		Spec: divergev1alpha1.PreviewGroupSpec{
			Owner: "alice",
			Routing: divergev1alpha1.PreviewGroupRouting{
				HeaderValue: "pg-hdr-42",
			},
		},
	}
	envOtherNS := &divergev1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-other-ns",
			Namespace: "other-ns",
			Annotations: map[string]string{
				"divergedev.com/created-by": "bob",
			},
		},
	}
	envOwnedByBob := &divergev1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-owned",
			Namespace: "default",
			Annotations: map[string]string{
				"divergedev.com/created-by": "bob",
			},
		},
		Spec: divergev1alpha1.EnvironmentSpec{
			Routing: divergev1alpha1.EnvironmentRouting{HeaderValue: "env-hdr-99"},
			Deploy:  divergev1alpha1.EnvironmentDeploy{ChangedServices: []string{"api"}},
		},
	}
	ciSharedEnv := &divergev1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mr-42-directory",
			Namespace: "default",
			Annotations: map[string]string{
				"divergedev.com/created-by": "gitlab-ci",
			},
		},
		Spec: divergev1alpha1.EnvironmentSpec{
			Routing: divergev1alpha1.EnvironmentRouting{HeaderValue: "mr-42"},
			ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
				ServiceName: "directory-service",
				Port:        8080,
			},
		},
	}
	ciSharedPG := &divergev1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mr-43",
		},
		Spec: divergev1alpha1.PreviewGroupSpec{
			Owner:   "gitlab-ci",
			Routing: divergev1alpha1.PreviewGroupRouting{HeaderValue: "mr-43"},
			Services: []divergev1alpha1.PreviewGroupServiceSpec{
				{Name: "directory-service", Namespace: "default"},
			},
		},
	}

	require.NoError(t, fx.tm.crdClient.Create(ctx, clusterPG))
	require.NoError(t, fx.tm.crdClient.Create(ctx, envOtherNS))
	require.NoError(t, fx.tm.crdClient.Create(ctx, envOwnedByBob))
	require.NoError(t, fx.tm.crdClient.Create(ctx, ciSharedEnv))
	require.NoError(t, fx.tm.crdClient.Create(ctx, ciSharedPG))

	userCtx := auth.ContextWithUserInfo(ctx, &auth.UserInfo{Username: "test"})
	// Environment in other-ns does not conflict with "default"
	require.NoError(t, fx.tm.validatePreviewOwnership(userCtx, &pb.TunnelRegister{
		PreviewId: "env-other-ns",
		Namespace: "default",
		Service:   "web",
	}))
	// Cluster-scoped PreviewGroup denies non-owner (both direct Name and HeaderValue)
	err = fx.tm.validatePreviewOwnership(userCtx, &pb.TunnelRegister{
		PreviewId: "pg-owned-cluster",
		Namespace: "default",
		Service:   "web",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owned by \"alice\"")

	err = fx.tm.validatePreviewOwnership(userCtx, &pb.TunnelRegister{
		PreviewId: "pg-hdr-42",
		Namespace: "default",
		Service:   "web",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owned by \"alice\"")

	// Environment in same namespace owned by bob denies non-owner, even for disjoint service when bob is not in SharedPreviewOwners
	err = fx.tm.validatePreviewOwnership(userCtx, &pb.TunnelRegister{
		PreviewId: "env-owned",
		Namespace: "default",
		Service:   "billing",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owned by \"bob\"")

	err = fx.tm.validatePreviewOwnership(userCtx, &pb.TunnelRegister{
		PreviewId: "env-hdr-99",
		Namespace: "default",
		Service:   "billing",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owned by \"bob\"")

	// SharedPreviewOwners ("gitlab-ci") allows unforced disjoint cross-service attachment for v3 only
	require.NoError(t, fx.tm.validatePreviewOwnership(userCtx, &pb.TunnelRegister{
		PreviewId:       "mr-42",
		Namespace:       "default",
		Service:         "billing-service",
		ProtocolVersion: 3,
	}))
	require.NoError(t, fx.tm.validatePreviewOwnership(userCtx, &pb.TunnelRegister{
		PreviewId:       "mr-43",
		Namespace:       "default",
		Service:         "billing-service",
		ProtocolVersion: 3,
	}))

	// Protocol v2 cannot share foreign previews even with SharedPreviewOwners
	err = fx.tm.validatePreviewOwnership(userCtx, &pb.TunnelRegister{
		PreviewId:       "mr-42",
		Namespace:       "default",
		Service:         "billing-service",
		ProtocolVersion: 2,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owned by \"gitlab-ci\"")

	// Colliding service or empty service is still blocked on CI-shared preview unless Force
	err = fx.tm.validatePreviewOwnership(userCtx, &pb.TunnelRegister{
		PreviewId:       "mr-42",
		Namespace:       "default",
		Service:         "directory-service",
		ProtocolVersion: 3,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owned by \"gitlab-ci\"")

	err = fx.tm.validatePreviewOwnership(userCtx, &pb.TunnelRegister{
		PreviewId:       "mr-43",
		Namespace:       "default",
		Service:         "",
		ProtocolVersion: 3,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owned by \"gitlab-ci\"")
}

func TestTunnelManager_V3StreamingTrailersAndDisconnectCancel(t *testing.T) {
	fx := setupTunnelTestFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sseReleaseRest := make(chan struct{})
	handlerCanceled := make(chan struct{}, 1)
	ready, _, err := dialV3TestTunnel(t, ctx, fx.client, &pb.TunnelRegister{
		Service:         "web",
		Namespace:       "default",
		Port:            3000,
		PreviewId:       "v3-stream",
		ProtocolVersion: 3,
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fl, _ := w.(http.Flusher)
			_, _ = fmt.Fprintf(w, "data: ev-0\n\n")
			if fl != nil {
				fl.Flush()
			}
			<-sseReleaseRest
			for i := 1; i < 3; i++ {
				_, _ = fmt.Fprintf(w, "data: ev-%d\n\n", i)
				if fl != nil {
					fl.Flush()
				}
			}
		case "/trailers":
			w.Header().Add("Trailer", "Grpc-Status")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("grpc-body"))
			w.Header().Set("Grpc-Status", "0")
		case "/hang":
			w.WriteHeader(http.StatusOK)
			if fl, ok := w.(http.Flusher); ok {
				_, _ = w.Write([]byte("hello"))
				fl.Flush()
			}
			<-r.Context().Done()
			select {
			case handlerCanceled <- struct{}{}:
			default:
			}
		}
	}))
	require.NoError(t, err)
	assert.Equal(t, int32(3), ready.ProtocolVersion)

	// 1. SSE flushes ev-0 immediately before remaining events are unblocked
	sseReq, _ := http.NewRequest(http.MethodGet, fx.proxyServer.URL+"/sse", nil)
	sseReq.Header.Set("X-Diverge-Env", "v3-stream")
	sseResp, err := fx.proxyServer.Client().Do(sseReq)
	require.NoError(t, err)
	buf := make([]byte, 128)
	n, err := sseResp.Body.Read(buf)
	require.NoError(t, err)
	assert.Contains(t, string(buf[:n]), "data: ev-0")
	close(sseReleaseRest)
	rest, err := io.ReadAll(sseResp.Body)
	_ = sseResp.Body.Close()
	require.NoError(t, err)
	assert.Contains(t, string(rest), "data: ev-2")

	// 2. HTTP/2 trailers
	trReq, _ := http.NewRequest(http.MethodPost, fx.proxyServer.URL+"/trailers", strings.NewReader("payload"))
	trReq.Header.Set("X-Diverge-Env", "v3-stream")
	trResp, err := fx.proxyServer.Client().Do(trReq)
	require.NoError(t, err)
	_, err = io.ReadAll(trResp.Body)
	_ = trResp.Body.Close()
	require.NoError(t, err)
	assert.Equal(t, "0", trResp.Trailer.Get("Grpc-Status"))

	// 3. Client disconnect cancels localhost handler promptly
	hangCtx, hangCancel := context.WithCancel(context.Background())
	hangReq, _ := http.NewRequestWithContext(hangCtx, http.MethodGet, fx.proxyServer.URL+"/hang", nil)
	hangReq.Header.Set("X-Diverge-Env", "v3-stream")
	hangResp, err := fx.proxyServer.Client().Do(hangReq)
	require.NoError(t, err)
	tmp := make([]byte, 16)
	_, err = hangResp.Body.Read(tmp)
	require.NoError(t, err)
	hangCancel()
	_ = hangResp.Body.Close()

	select {
	case <-handlerCanceled:
	case <-time.After(time.Second):
		t.Fatal("expected downstream client disconnect to cancel localhost handler")
	}
}

func TestTunnelManager_V3MultiServiceAndHeaderPrecedence(t *testing.T) {
	fx := setupTunnelTestFixtureWithConfig(t, func(cfg *TunnelManagerConfig) {
		cfg.DefaultNamespace = "apps"
		cfg.ProxyServiceAddr = "diverge-tunnel-proxy.diverge-system.svc:8081"
	}, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Empty namespace falls back to DefaultNamespace ("apps")
	readyA, _, err := dialV3TestTunnel(t, ctx, fx.client, &pb.TunnelRegister{
		Service:         "svc-a",
		Port:            8081,
		PreviewId:       "multi-prev",
		ProtocolVersion: 3,
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("from-svc-a"))
	}))
	require.NoError(t, err)
	assert.Equal(t, "diverge-tunnel-proxy.diverge-system.svc:8081", readyA.ProxyService)
	assert.NotEmpty(t, readyA.K8SServiceName)

	readyB, _, err := dialV3TestTunnel(t, ctx, fx.client, &pb.TunnelRegister{
		Service:         "svc-b",
		Namespace:       "apps",
		Port:            8082,
		PreviewId:       "multi-prev",
		ProtocolVersion: 3,
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("from-svc-b"))
	}))
	require.NoError(t, err)
	assert.NotEqual(t, readyA.K8SServiceName, readyB.K8SServiceName)

	// 1. Explicit X-Diverge-Service routing + Host with diverge-tunnel-proxy.diverge-system.svc must NOT poison namespace
	reqA, _ := http.NewRequest(http.MethodGet, fx.proxyServer.URL+"/", nil)
	reqA.Host = "diverge-tunnel-proxy.diverge-system.svc.cluster.local:8081"
	reqA.Header.Set("X-Diverge-Env", "multi-prev")
	reqA.Header.Set("X-Diverge-Service", "svc-a")
	respA, err := fx.proxyServer.Client().Do(reqA)
	require.NoError(t, err)
	bodyA, _ := io.ReadAll(respA.Body)
	_ = respA.Body.Close()
	assert.Equal(t, http.StatusOK, respA.StatusCode)
	assert.Equal(t, "from-svc-a", string(bodyA))

	// 2. Host matching K8SServiceName without X-Diverge-Service routes to svc-b (with or without X-Diverge-Env)
	assert.True(t, strings.HasPrefix(readyB.K8SServiceName, "diverge-tsvc-"))
	reqHostB, _ := http.NewRequest(http.MethodGet, fx.proxyServer.URL+"/", nil)
	reqHostB.Host = readyB.K8SServiceName + ".apps.svc.cluster.local:8081"
	respB, err := fx.proxyServer.Client().Do(reqHostB)
	require.NoError(t, err)
	bodyB, _ := io.ReadAll(respB.Body)
	_ = respB.Body.Close()
	assert.Equal(t, http.StatusOK, respB.StatusCode)
	assert.Equal(t, "from-svc-b", string(bodyB))

	reqHostBWithEnv, _ := http.NewRequest(http.MethodGet, fx.proxyServer.URL+"/", nil)
	reqHostBWithEnv.Host = readyB.K8SServiceName + ".apps.svc.cluster.local:8081"
	reqHostBWithEnv.Header.Set("X-Diverge-Env", "multi-prev")
	respBEnv, err := fx.proxyServer.Client().Do(reqHostBWithEnv)
	require.NoError(t, err)
	bodyBEnv, _ := io.ReadAll(respBEnv.Body)
	_ = respBEnv.Body.Close()
	assert.Equal(t, http.StatusOK, respBEnv.StatusCode)
	assert.Equal(t, "from-svc-b", string(bodyBEnv))

	// 3. Omitting X-Diverge-Service inside ambiguous namespace returns 400 Bad Request
	reqAmbig, _ := http.NewRequest(http.MethodGet, fx.proxyServer.URL+"/", nil)
	reqAmbig.Header.Set("X-Diverge-Env", "multi-prev")
	reqAmbig.Header.Set("X-Diverge-Namespace", "apps")
	respAmbig, err := fx.proxyServer.Client().Do(reqAmbig)
	require.NoError(t, err)
	_ = respAmbig.Body.Close()
	assert.Equal(t, http.StatusBadRequest, respAmbig.StatusCode)
}

func TestTunnelManager_V3ProxyGuards(t *testing.T) {
	fx := setupTunnelTestFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var seenLeakedDivergeHeader atomic.Bool
	_, _, err := dialV3TestTunnel(t, ctx, fx.client, &pb.TunnelRegister{
		Service:         "api",
		Namespace:       "default",
		Port:            8080,
		PreviewId:       "guards-prev",
		ProtocolVersion: 3,
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k := range r.Header {
			if strings.HasPrefix(strings.ToLower(k), "x-diverge-") {
				seenLeakedDivergeHeader.Store(true)
			}
		}
		if r.URL.Path == "/bad-status" {
			w.WriteHeader(600)
			return
		}
		if r.URL.Path == "/echo-size" {
			n, _ := io.Copy(io.Discard, r.Body)
			w.Header().Set("X-Diverge-Secret", "should-strip")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, "bytes=%d", n)
			return
		}
		w.Header().Set("X-Diverge-Leak", "nope")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	require.NoError(t, err)

	// 1. X-Diverge-* headers stripped on request & response
	req1, _ := http.NewRequest(http.MethodGet, fx.proxyServer.URL+"/ok", nil)
	req1.Header.Set("X-Diverge-Env", "guards-prev")
	req1.Header.Set("X-Diverge-Internal", "secret")
	resp1, err := fx.proxyServer.Client().Do(req1)
	require.NoError(t, err)
	_ = resp1.Body.Close()
	assert.Equal(t, http.StatusOK, resp1.StatusCode)
	assert.False(t, seenLeakedDivergeHeader.Load(), "X-Diverge-* request headers must be stripped")
	assert.Empty(t, resp1.Header.Get("X-Diverge-Leak"), "X-Diverge-* response headers must be stripped")

	// 2. Request body > 10 MiB returns 413 (both Content-Length and unknown ContentLength)
	bigReq, _ := http.NewRequest(http.MethodPost, fx.proxyServer.URL+"/echo-size", bytes.NewReader(make([]byte, (10<<20)+100)))
	bigReq.Header.Set("X-Diverge-Env", "guards-prev")
	bigResp, err := fx.proxyServer.Client().Do(bigReq)
	require.NoError(t, err)
	_ = bigResp.Body.Close()
	assert.Equal(t, http.StatusRequestEntityTooLarge, bigResp.StatusCode)

	pr, pw := io.Pipe()
	go func() {
		buf := make([]byte, 1<<20)
		for i := 0; i < 11; i++ {
			if _, wErr := pw.Write(buf); wErr != nil {
				return
			}
		}
		_ = pw.Close()
	}()
	streamBigReq, _ := http.NewRequest(http.MethodPost, fx.proxyServer.URL+"/echo-size", pr)
	streamBigReq.ContentLength = -1
	streamBigReq.Header.Set("X-Diverge-Env", "guards-prev")
	streamBigResp, err := fx.proxyServer.Client().Do(streamBigReq)
	require.NoError(t, err)
	_ = streamBigResp.Body.Close()
	assert.Equal(t, http.StatusRequestEntityTooLarge, streamBigResp.StatusCode)

	// 3. Status code outside [200, 599] rejected -> 502 Bad Gateway
	badStatusReq, _ := http.NewRequest(http.MethodGet, fx.proxyServer.URL+"/bad-status", nil)
	badStatusReq.Header.Set("X-Diverge-Env", "guards-prev")
	badStatusResp, err := fx.proxyServer.Client().Do(badStatusReq)
	require.NoError(t, err)
	_ = badStatusResp.Body.Close()
	assert.Equal(t, http.StatusBadGateway, badStatusResp.StatusCode)

	// 4. Concurrency cap returns 503 when semaphore is full
	at, _, ok := fx.tm.findActiveTunnel("default", "guards-prev", "", "")
	require.True(t, ok)
	require.Error(t, at.proxy.ModifyResponse(&http.Response{StatusCode: http.StatusSwitchingProtocols}))
	require.Error(t, at.proxy.ModifyResponse(&http.Response{StatusCode: 199}))
	for i := 0; i < cap(at.sem); i++ {
		at.sem <- struct{}{}
	}
	concReq, _ := http.NewRequest(http.MethodGet, fx.proxyServer.URL+"/ok", nil)
	concReq.Header.Set("X-Diverge-Env", "guards-prev")
	concResp, err := fx.proxyServer.Client().Do(concReq)
	require.NoError(t, err)
	_ = concResp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, concResp.StatusCode)
	for i := 0; i < cap(at.sem); i++ {
		<-at.sem
	}
}

func TestTunnelManager_ClientIDFencingAndCrossUserForce(t *testing.T) {
	var currentUser atomic.Value
	currentUser.Store("alice")
	var allowDelete atomic.Bool

	fx := setupTunnelTestFixtureWithConfig(t, nil, func(sar *authorizationv1.SubjectAccessReview) {
		if sar.Spec.ResourceAttributes != nil && sar.Spec.ResourceAttributes.Verb == "delete" {
			sar.Status.Allowed = allowDelete.Load()
			return
		}
		sar.Status.Allowed = true
	}, func(_ *http.Request) *auth.UserInfo {
		return &auth.UserInfo{Username: currentUser.Load().(string)}
	})

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	_, conn1, err := dialV3TestTunnel(t, ctx1, fx.client, &pb.TunnelRegister{
		Service:          "api",
		Namespace:        "default",
		Port:             8080,
		PreviewId:        "fence-prev",
		ProtocolVersion:  3,
		ClientInstanceId: "laptop-1",
	}, http.NotFoundHandler())
	require.NoError(t, err)

	// 1. Different client_instance_id without Force -> CodeAlreadyExists
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	_, _, err = dialV3TestTunnel(t, ctx2, fx.client, &pb.TunnelRegister{
		Service:          "api",
		Namespace:        "default",
		Port:             8080,
		PreviewId:        "fence-prev",
		ProtocolVersion:  3,
		ClientInstanceId: "laptop-2",
	}, http.NotFoundHandler())
	require.Error(t, err)
	assert.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))

	// 2. Same user + same client_instance_id silently reconnects without Force and evicts old conn
	ctx1b, cancel1b := context.WithCancel(context.Background())
	defer cancel1b()
	_, conn1b, err := dialV3TestTunnel(t, ctx1b, fx.client, &pb.TunnelRegister{
		Service:          "api",
		Namespace:        "default",
		Port:             8080,
		PreviewId:        "fence-prev",
		ProtocolVersion:  3,
		ClientInstanceId: "laptop-1",
	}, http.NotFoundHandler())
	require.NoError(t, err)
	require.True(t, conn1.WaitDone(time.Second))
	evt, hasEvt := conn1.RemoteClose()
	require.True(t, hasEvt)
	assert.Equal(t, pb.CloseCode_CLOSE_CODE_EVICTED, evt.Code)

	// 3. Cross-user Force requires delete SAR
	currentUser.Store("bob")
	ctxBobDeny, cancelBobDeny := context.WithCancel(context.Background())
	defer cancelBobDeny()
	_, _, err = dialV3TestTunnel(t, ctxBobDeny, fx.client, &pb.TunnelRegister{
		Service:          "api",
		Namespace:        "default",
		Port:             8080,
		PreviewId:        "fence-prev",
		ProtocolVersion:  3,
		ClientInstanceId: "bob-laptop",
		Force:            true,
	}, http.NotFoundHandler())
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// 4. Cross-user Force succeeds when both update + delete SAR are allowed, evicting alice
	allowDelete.Store(true)
	ctxBob, cancelBob := context.WithCancel(context.Background())
	defer cancelBob()
	_, _, err = dialV3TestTunnel(t, ctxBob, fx.client, &pb.TunnelRegister{
		Service:          "api",
		Namespace:        "default",
		Port:             8080,
		PreviewId:        "fence-prev",
		ProtocolVersion:  3,
		ClientInstanceId: "bob-laptop",
		Force:            true,
	}, http.NotFoundHandler())
	require.NoError(t, err)
	require.True(t, conn1b.WaitDone(time.Second))
	evtBob, hasEvtBob := conn1b.RemoteClose()
	require.True(t, hasEvtBob)
	assert.Equal(t, pb.CloseCode_CLOSE_CODE_EVICTED, evtBob.Code)
}

func TestTunnelManager_AuthHygieneAndRevocation(t *testing.T) {
	var sarAllowed atomic.Bool
	sarAllowed.Store(true)
	var nextUser atomic.Value
	nextUser.Store(&auth.UserInfo{Username: "test"})

	fx := setupTunnelTestFixtureWithConfig(t, func(cfg *TunnelManagerConfig) {
		cfg.AllowedNamespaces = []string{"default"}
		cfg.StreamLimiter = NewStreamLimiter(2, 1)
		cfg.SARRecheckInterval = 30 * time.Millisecond
		cfg.MaxTunnelLifetime = time.Hour
	}, func(sar *authorizationv1.SubjectAccessReview) {
		sar.Status.Allowed = sarAllowed.Load()
	}, func(_ *http.Request) *auth.UserInfo {
		return nextUser.Load().(*auth.UserInfo)
	})

	// 1. Cookie-authenticated stream rejected
	nextUser.Store(&auth.UserInfo{Username: "alice", FromCookie: true})
	_, _, err := dialV3TestTunnel(t, context.Background(), fx.client, &pb.TunnelRegister{
		Service: "web", Namespace: "default", Port: 8080, PreviewId: "p1", ProtocolVersion: 3,
	}, http.NotFoundHandler())
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	// 2. Macaroon capability token rejected
	nextUser.Store(&auth.UserInfo{
		Username: "system:agenttask:default:t1",
		AuthType: "macaroon",
		Extra:    map[string]authorizationv1.ExtraValue{auth.ExtraKeyAuthType: {"macaroon"}},
	})
	_, _, err = dialV3TestTunnel(t, context.Background(), fx.client, &pb.TunnelRegister{
		Service: "web", Namespace: "default", Port: 8080, PreviewId: "p1", ProtocolVersion: 3,
	}, http.NotFoundHandler())
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// 3. Expired token rejected
	nextUser.Store(&auth.UserInfo{Username: "alice", ExpiresAt: time.Now().Add(-time.Second)})
	_, _, err = dialV3TestTunnel(t, context.Background(), fx.client, &pb.TunnelRegister{
		Service: "web", Namespace: "default", Port: 8080, PreviewId: "p1", ProtocolVersion: 3,
	}, http.NotFoundHandler())
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	// 4. Disallowed namespace, invalid port, and invalid metadata rejected
	nextUser.Store(&auth.UserInfo{Username: "alice"})
	_, _, err = dialV3TestTunnel(t, context.Background(), fx.client, &pb.TunnelRegister{
		Service: "web", Namespace: "kube-system", Port: 8080, PreviewId: "p1", ProtocolVersion: 3,
	}, http.NotFoundHandler())
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_, _, err = dialV3TestTunnel(t, context.Background(), fx.client, &pb.TunnelRegister{
		Service: "web", Namespace: "default", Port: 70000, PreviewId: "p1", ProtocolVersion: 3,
	}, http.NotFoundHandler())
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, _, err = dialV3TestTunnel(t, context.Background(), fx.client, &pb.TunnelRegister{
		Service: "web", Namespace: "default", Port: 8080, PreviewId: "p1", ProtocolVersion: 3,
		Metadata: map[string]string{"InvalidKey": "1"},
	}, http.NotFoundHandler())
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// 5. Short token expiry closes active tunnel with CLOSE_CODE_AUTH_EXPIRED; StreamLimiter enforces per-user limit 1
	nextUser.Store(&auth.UserInfo{Username: "expiring-user", ExpiresAt: time.Now().Add(120 * time.Millisecond)})
	ctxExp, cancelExp := context.WithCancel(context.Background())
	defer cancelExp()
	_, connExp, err := dialV3TestTunnel(t, ctxExp, fx.client, &pb.TunnelRegister{
		Service: "web", Namespace: "default", Port: 8080, PreviewId: "exp-prev", ProtocolVersion: 3,
	}, http.NotFoundHandler())
	require.NoError(t, err)

	// Second concurrent stream for same user hits per-user StreamLimiter cap
	_, _, err = dialV3TestTunnel(t, ctxExp, fx.client, &pb.TunnelRegister{
		Service: "api", Namespace: "default", Port: 8081, PreviewId: "exp-prev", ProtocolVersion: 3,
	}, http.NotFoundHandler())
	assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))

	require.True(t, connExp.WaitDone(time.Second))
	evtExp, okExp := connExp.RemoteClose()
	require.True(t, okExp)
	assert.Equal(t, pb.CloseCode_CLOSE_CODE_AUTH_EXPIRED, evtExp.Code)

	// 6. Periodic SAR revocation closes active tunnel with CLOSE_CODE_AUTH_EXPIRED
	nextUser.Store(&auth.UserInfo{Username: "revoked-user"})
	ctxSAR, cancelSAR := context.WithCancel(context.Background())
	defer cancelSAR()
	_, connSAR, err := dialV3TestTunnel(t, ctxSAR, fx.client, &pb.TunnelRegister{
		Service: "web", Namespace: "default", Port: 8080, PreviewId: "sar-prev", ProtocolVersion: 3,
	}, http.NotFoundHandler())
	require.NoError(t, err)

	sarAllowed.Store(false)
	require.True(t, connSAR.WaitDone(time.Second))
	evtSAR, okSAR := connSAR.RemoteClose()
	require.True(t, okSAR)
	assert.Equal(t, pb.CloseCode_CLOSE_CODE_AUTH_EXPIRED, evtSAR.Code)
}

func TestTunnelManager_ResourcesOwnerRefAndGracefulShutdown(t *testing.T) {
	ctx := context.Background()

	// 1. SkipK8SService skips Service/EndpointSlice/Endpoints
	fxSkip := setupTunnelTestFixture(t)
	skipCtx, skipCancel := context.WithCancel(ctx)
	readySkip, _, err := dialV3TestTunnel(t, skipCtx, fxSkip.client, &pb.TunnelRegister{
		Service:         "web",
		Namespace:       "default",
		Port:            3000,
		PreviewId:       "skip-prev",
		ProtocolVersion: 3,
		SkipK8SService:  true,
	}, http.NotFoundHandler())
	require.NoError(t, err)
	assert.Empty(t, readySkip.K8SServiceName)
	svcs, err := fxSkip.fakeK8s.CoreV1().Services("default").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, svcs.Items)
	skipCancel()

	// 2. CreatePerTunnelServices=false skips Service creation even without SkipK8SService
	fxNoSvc := setupTunnelTestFixtureWithConfig(t, func(cfg *TunnelManagerConfig) {
		cfg.CreatePerTunnelServices = ptr.To(false)
	}, nil, nil)
	noSvcCtx, noSvcCancel := context.WithCancel(ctx)
	readyNoSvc, _, err := dialV3TestTunnel(t, noSvcCtx, fxNoSvc.client, &pb.TunnelRegister{
		Service:         "web",
		Namespace:       "default",
		Port:            3000,
		PreviewId:       "nosvc-prev",
		ProtocolVersion: 3,
	}, http.NotFoundHandler())
	require.NoError(t, err)
	assert.Empty(t, readyNoSvc.K8SServiceName)
	svcs2, err := fxNoSvc.fakeK8s.CoreV1().Services("default").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, svcs2.Items)
	noSvcCancel()

	// 3. Enabled creates OwnerReference -> Lease without tunnel-expires, and Shutdown drains in-flight request
	fx := setupTunnelTestFixture(t)
	inFlightStarted := make(chan struct{})
	inFlightRelease := make(chan struct{})
	tunCtx, tunCancel := context.WithCancel(ctx)
	defer tunCancel()

	ready, conn, err := dialV3TestTunnel(t, tunCtx, fx.client, &pb.TunnelRegister{
		Service:         "api",
		Namespace:       "default",
		Port:            8080,
		PreviewId:       "drain-prev",
		ProtocolVersion: 3,
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(inFlightStarted)
		<-inFlightRelease
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("drained-ok"))
	}))
	require.NoError(t, err)
	require.NotEmpty(t, ready.K8SServiceName)

	// Verify OwnerReference on Service & no tunnel-expires on Lease/Service
	svcObj, err := fx.fakeK8s.CoreV1().Services("default").Get(ctx, ready.K8SServiceName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, svcObj.OwnerReferences, 1)
	assert.Equal(t, "Lease", svcObj.OwnerReferences[0].Kind)
	_, svcHasExpires := svcObj.Annotations["divergedev.com/tunnel-expires"]
	assert.False(t, svcHasExpires)

	leaseName := tunnelLeaseName("drain-prev", "api")
	leaseObj, err := fx.fakeK8s.CoordinationV1().Leases("default").Get(ctx, leaseName, metav1.GetOptions{})
	require.NoError(t, err)
	_, leaseHasExpires := leaseObj.Annotations["divergedev.com/tunnel-expires"]
	assert.False(t, leaseHasExpires)

	// Start in-flight HTTP request before Shutdown
	reqDone := make(chan *http.Response, 1)
	reqErrCh := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, fx.proxyServer.URL+"/slow", nil)
		req.Header.Set("X-Diverge-Env", "drain-prev")
		resp, reqErr := fx.proxyServer.Client().Do(req)
		if reqErr != nil {
			reqErrCh <- reqErr
			return
		}
		reqDone <- resp
	}()

	<-inFlightStarted
	shutDone := make(chan struct{})
	go func() {
		defer close(shutDone)
		shutCtx, shutCancel := context.WithTimeout(ctx, 3*time.Second)
		defer shutCancel()
		fx.tm.Shutdown(shutCtx)
	}()

	// IsReady() must flip false immediately once Shutdown begins
	assert.Eventually(t, func() bool { return !fx.tm.IsReady() }, time.Second, 10*time.Millisecond)

	// Release in-flight request and ensure it completed cleanly BEFORE CLOSE_CODE_SERVER_SHUTDOWN
	close(inFlightRelease)
	select {
	case resp := <-reqDone:
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "drained-ok", string(body))
	case reqErr := <-reqErrCh:
		t.Fatalf("in-flight request failed during graceful shutdown: %v", reqErr)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for in-flight request during shutdown")
	}

	select {
	case <-shutDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not finish")
	}

	require.True(t, conn.WaitDone(time.Second))
	evt, ok := conn.RemoteClose()
	require.True(t, ok)
	assert.Equal(t, pb.CloseCode_CLOSE_CODE_SERVER_SHUTDOWN, evt.Code)

	// Lease should be released cleanly by Shutdown
	assert.Eventually(t, func() bool {
		_, getErr := fx.fakeK8s.CoordinationV1().Leases("default").Get(ctx, leaseName, metav1.GetOptions{})
		return getErr != nil
	}, time.Second, 20*time.Millisecond)
}

func TestTunnelManager_V3TrailerStripping(t *testing.T) {
	fx := setupTunnelTestFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, _, err := dialV3TestTunnel(t, ctx, fx.client, &pb.TunnelRegister{
		Service:         "api",
		Namespace:       "default",
		Port:            8080,
		PreviewId:       "trailer-prev",
		ProtocolVersion: 3,
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Trailer", "X-Diverge-Secret, Grpc-Status")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("payload"))
		w.Header().Set("Grpc-Status", "0")
		w.Header().Set("X-Diverge-Secret", "sensitive")
		w.Header().Set(http.TrailerPrefix+"X-Diverge-Sneaky", "hidden")
	}))
	require.NoError(t, err)

	req, _ := http.NewRequest(http.MethodPost, fx.proxyServer.URL+"/rpc", strings.NewReader("body"))
	req.Header.Set("X-Diverge-Env", "trailer-prev")
	resp, err := fx.proxyServer.Client().Do(req)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)

	assert.Equal(t, "0", resp.Trailer.Get("Grpc-Status"))
	assert.Empty(t, resp.Trailer.Get("X-Diverge-Secret"))
	assert.Empty(t, resp.Trailer.Get("X-Diverge-Sneaky"))
	for _, tv := range resp.Header.Values("Trailer") {
		assert.NotContains(t, strings.ToLower(tv), "x-diverge-")
	}
}

func TestTunnelManager_RegistrationMissingAndInvalidClientID(t *testing.T) {
	fx := setupTunnelTestFixture(t)
	ctx := context.Background()

	// 1. Non-Register first message -> CodeInvalidArgument
	streamNoReg := fx.client.Tunnel(ctx)
	require.NoError(t, streamNoReg.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Pong{
			Pong: &pb.TunnelPong{},
		},
	}))
	_, err := streamNoReg.Receive()
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// 2. Overlong ClientInstanceId (>128 chars) -> CodeInvalidArgument
	streamLong := fx.client.Tunnel(ctx)
	require.NoError(t, streamLong.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Register{
			Register: &pb.TunnelRegister{
				PreviewId:        "cid-prev",
				Namespace:        "default",
				Service:          "api",
				Port:             8080,
				ClientInstanceId: strings.Repeat("a", 129),
			},
		},
	}))
	_, err = streamLong.Receive()
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// 3. Control char in ClientInstanceId -> CodeInvalidArgument
	streamCtrl := fx.client.Tunnel(ctx)
	require.NoError(t, streamCtrl.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Register{
			Register: &pb.TunnelRegister{
				PreviewId:        "cid-prev",
				Namespace:        "default",
				Service:          "api",
				Port:             8080,
				ClientInstanceId: "lap\ntop",
			},
		},
	}))
	_, err = streamCtrl.Receive()
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestTunnelManager_ConditionalDeleteTunnelResources(t *testing.T) {
	fx := setupTunnelTestFixture(t)
	ctx := context.Background()
	svcName := tunnelServiceName("cond-prev", "api")
	labels := map[string]string{"divergedev.com/tunnel": "true"}
	ann := map[string]string{"divergedev.com/tunnel-id": "new-tunnel-id"}

	_, err := fx.fakeK8s.CoreV1().Services("default").Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: "default", UID: "s-1", ResourceVersion: "1", Labels: labels, Annotations: ann},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = fx.fakeK8s.DiscoveryV1().EndpointSlices("default").Create(ctx, &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: svcName, Namespace: "default", UID: "e-1", ResourceVersion: "1", Labels: labels, Annotations: ann},
		AddressType: discoveryv1.AddressTypeIPv4,
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = fx.fakeK8s.CoreV1().Endpoints("default").Create(ctx, &corev1.Endpoints{ //nolint:staticcheck // testing legacy Endpoints fencing
		ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: "default", UID: "ep-1", ResourceVersion: "1", Labels: labels, Annotations: ann},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Stale tunnel-id must not delete resources owned by replacement tunnel
	fx.tm.deleteTunnelResources(ctx, "default", svcName, "old-tunnel-id")
	_, err = fx.fakeK8s.CoreV1().Services("default").Get(ctx, svcName, metav1.GetOptions{})
	assert.NoError(t, err)
	_, err = fx.fakeK8s.DiscoveryV1().EndpointSlices("default").Get(ctx, svcName, metav1.GetOptions{})
	assert.NoError(t, err)
	_, err = fx.fakeK8s.CoreV1().Endpoints("default").Get(ctx, svcName, metav1.GetOptions{}) //nolint:staticcheck // testing legacy Endpoints fencing
	assert.NoError(t, err)

	// Matching tunnel-id deletes all three
	fx.tm.deleteTunnelResources(ctx, "default", svcName, "new-tunnel-id")
	_, err = fx.fakeK8s.CoreV1().Services("default").Get(ctx, svcName, metav1.GetOptions{})
	assert.Error(t, err)
	_, err = fx.fakeK8s.DiscoveryV1().EndpointSlices("default").Get(ctx, svcName, metav1.GetOptions{})
	assert.Error(t, err)
	_, err = fx.fakeK8s.CoreV1().Endpoints("default").Get(ctx, svcName, metav1.GetOptions{}) //nolint:staticcheck // testing legacy Endpoints fencing
	assert.Error(t, err)
}

func TestTunnelManager_PreviewGroupForceRequiresDeleteAndServiceNamespaceSAR(t *testing.T) {
	var denyKey atomic.Value
	denyKey.Store("")
	fx := setupTunnelTestFixtureWithConfig(t, nil, func(sar *authorizationv1.SubjectAccessReview) {
		ra := sar.Spec.ResourceAttributes
		if ra != nil {
			key := ra.Verb + ":" + ra.Resource + ":" + ra.Namespace
			if key == denyKey.Load().(string) {
				sar.Status.Allowed = false
				return
			}
		}
		sar.Status.Allowed = true
	}, nil)

	ctx := context.Background()
	pg := &divergev1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-force-check"},
		Spec: divergev1alpha1.PreviewGroupSpec{
			Owner:    "alice",
			Services: []divergev1alpha1.PreviewGroupServiceSpec{{Name: "api", Namespace: "svc-ns"}},
		},
	}
	require.NoError(t, fx.tm.crdClient.Create(ctx, pg))
	bobCtx := auth.ContextWithUserInfo(ctx, &auth.UserInfo{Username: "bob"})

	// Denying delete on svc-ns must reject Force
	denyKey.Store("delete:environments:svc-ns")
	err := fx.tm.validatePreviewOwnership(bobCtx, &pb.TunnelRegister{
		PreviewId:       "pg-force-check",
		Namespace:       "default",
		Service:         "api",
		ProtocolVersion: 3,
		Force:           true,
	})
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// Denying delete on previewgroups (both cluster and namespace) must reject Force
	denyKey.Store("delete:previewgroups:default")
	// Use a custom SAR override that denies both cluster "" and "default" previewgroups delete
	fxBoth := setupTunnelTestFixtureWithConfig(t, nil, func(sar *authorizationv1.SubjectAccessReview) {
		ra := sar.Spec.ResourceAttributes
		if ra != nil && ra.Verb == "delete" && ra.Resource == "previewgroups" {
			sar.Status.Allowed = false
			return
		}
		sar.Status.Allowed = true
	}, nil)
	pgCopy := pg.DeepCopy()
	pgCopy.ResourceVersion = ""
	require.NoError(t, fxBoth.tm.crdClient.Create(ctx, pgCopy))
	err = fxBoth.tm.validatePreviewOwnership(bobCtx, &pb.TunnelRegister{
		PreviewId:       "pg-force-check",
		Namespace:       "default",
		Service:         "api",
		ProtocolVersion: 3,
		Force:           true,
	})
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// Allowing all verbs passes
	denyKey.Store("")
	require.NoError(t, fx.tm.validatePreviewOwnership(bobCtx, &pb.TunnelRegister{
		PreviewId:       "pg-force-check",
		Namespace:       "default",
		Service:         "api",
		ProtocolVersion: 3,
		Force:           true,
	}))
}

func TestTunnelManager_V2StripsHopByHopAndDivergeHeaders(t *testing.T) {
	fx := setupTunnelTestFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := fx.client.Tunnel(ctx)
	require.NoError(t, stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Register{
			Register: &pb.TunnelRegister{
				PreviewId:       "v2-hop-prev",
				Namespace:       "default",
				Port:            8080,
				ProtocolVersion: 2,
			},
		},
	}))
	msg, err := stream.Receive()
	require.NoError(t, err)
	require.NotNil(t, msg.GetReady())

	reqHeadersSeen := make(chan map[string]string, 1)
	go func() {
		for {
			in, recvErr := stream.Receive()
			if recvErr != nil {
				return
			}
			if httpReq := in.GetHttpRequest(); httpReq != nil {
				seen := make(map[string]string, len(httpReq.Headers))
				for _, h := range httpReq.Headers {
					seen[strings.ToLower(h.Key)] = strings.Join(h.Values, ",")
				}
				reqHeadersSeen <- seen
				_ = stream.Send(&pb.TunnelServiceTunnelRequest{
					Payload: &pb.TunnelServiceTunnelRequest_HttpResponse{
						HttpResponse: &pb.TunnelHTTPResponse{
							RequestId:  httpReq.RequestId,
							StatusCode: http.StatusOK,
							Headers: []*pb.HeaderEntry{
								{Key: "Connection", Values: []string{"X-Hop-Resp"}},
								{Key: "X-Hop-Resp", Values: []string{"secret-hop"}},
								{Key: "Keep-Alive", Values: []string{"timeout=5"}},
								{Key: "X-Diverge-Internal", Values: []string{"leak"}},
								{Key: "X-App-Resp", Values: []string{"preserved"}},
							},
							Body: []byte("ok"),
						},
					},
				})
				return
			}
		}
	}()

	req := httptest.NewRequest(http.MethodGet, "http://tunnel.local/check", nil)
	req.Header.Set("X-Diverge-Env", "v2-hop-prev")
	req.Header.Set("X-Diverge-Secret", "drop-me")
	req.Header.Set("Connection", "X-Custom-Hop")
	req.Header.Set("X-Custom-Hop", "hop-val")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("X-App-Req", "keep-me")

	rec := httptest.NewRecorder()
	fx.tm.NewTunnelProxyHandler().ServeHTTP(rec, req)
	resp := rec.Result()
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	seen := <-reqHeadersSeen
	assert.Equal(t, "keep-me", seen["x-app-req"])
	assert.NotContains(t, seen, "x-diverge-env")
	assert.NotContains(t, seen, "x-diverge-secret")
	assert.NotContains(t, seen, "connection")
	assert.NotContains(t, seen, "x-custom-hop")
	assert.NotContains(t, seen, "keep-alive")

	assert.Equal(t, "preserved", resp.Header.Get("X-App-Resp"))
	assert.Empty(t, resp.Header.Get("X-Hop-Resp"))
	assert.Empty(t, resp.Header.Get("X-Diverge-Internal"))
}

func TestTunnelManager_ShutdownEvictsEarlyHandshake(t *testing.T) {
	fx := setupTunnelTestFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Hold an incoming Tunnel() call inside Lease acquisition (admitted, before registerActiveTunnel)
	// until Shutdown fires and cancels all non-active admitted contexts.
	earlyLeaseName := tunnelLeaseName("early-shut", "api")
	inAdmission := make(chan struct{})
	releaseGate := make(chan struct{})
	var gated sync.Once
	fx.fakeK8s.PrependReactor("get", "leases", func(action k8stesting.Action) (bool, runtime.Object, error) {
		ga := action.(k8stesting.GetAction)
		if ga.GetName() == earlyLeaseName {
			gated.Do(func() { close(inAdmission) })
			<-releaseGate
			return true, nil, context.Canceled
		}
		return false, nil, nil
	})

	dialErrCh := make(chan error, 1)
	go func() {
		_, _, err := dialV3TestTunnel(t, ctx, fx.client, &pb.TunnelRegister{
			Service: "api", Namespace: "default", Port: 8080, PreviewId: "early-shut", ProtocolVersion: 3,
		}, http.NotFoundHandler())
		dialErrCh <- err
	}()

	select {
	case <-inAdmission:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tunnel admission before Shutdown")
	}

	fx.tm.mu.RLock()
	admittedCount := len(fx.tm.admitted)
	activeCount := len(fx.tm.tunnels)
	fx.tm.mu.RUnlock()
	assert.Equal(t, 1, admittedCount)
	assert.Equal(t, 0, activeCount)

	shutCtx, shutCancel := context.WithTimeout(ctx, 2*time.Second)
	defer shutCancel()
	shutDone := make(chan struct{})
	go func() {
		defer close(shutDone)
		fx.tm.Shutdown(shutCtx)
	}()

	// Unblock fakeK8s reactor once Shutdown's earlyCancels cancel admitCtx or after brief check.
	assert.Eventually(t, func() bool { return !fx.tm.IsReady() }, time.Second, 5*time.Millisecond)
	close(releaseGate)

	select {
	case <-shutDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown blocked on pre-registration tunnel")
	}
	assert.NoError(t, shutCtx.Err())

	select {
	case err := <-dialErrCh:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("early-handshake stream did not exit after Shutdown")
	}

	// New Tunnel() requests after Shutdown are rejected with CodeUnavailable.
	_, _, err := dialV3TestTunnel(t, ctx, fx.client, &pb.TunnelRegister{
		Service: "api", Namespace: "default", Port: 8080, PreviewId: "after-shut", ProtocolVersion: 3,
	}, http.NotFoundHandler())
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}
