package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	authorizationv1 "k8s.io/api/authorization/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	clifake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	divergev1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/server/auth"
	"github.com/divergedev/diverge/internal/server/streaming"
)

type headerInjectTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerInjectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	for k, v := range t.headers {
		clone.Header.Set(k, v)
	}
	return t.base.RoundTrip(clone)
}

type stubTunnelDirectory struct {
	records []TunnelRecord
}

func (s *stubTunnelDirectory) Lookup(namespace, previewID, service string) (*TunnelRecord, bool) {
	for i := range s.records {
		rec := s.records[i]
		if rec.Namespace == namespace && rec.PreviewID == previewID && rec.Service == service {
			return &rec, true
		}
	}
	return nil, false
}

func (s *stubTunnelDirectory) List(filter TunnelFilter) []TunnelRecord {
	var out []TunnelRecord
	for _, rec := range s.records {
		if filter.Namespace != "" && rec.Namespace != filter.Namespace {
			continue
		}
		if filter.PreviewID != "" && rec.PreviewID != filter.PreviewID {
			continue
		}
		if filter.Service != "" && rec.Service != filter.Service {
			continue
		}
		out = append(out, rec)
	}
	return out
}

func setupResolverFixture(
	t *testing.T,
	envs []*divergev1alpha1.Environment,
	tmCfgFn func(*TunnelManagerConfig),
	sarFn func(*authorizationv1.SubjectAccessReview),
	userFn func(*http.Request) *auth.UserInfo,
) (*tunnelTestFixture, *httptest.Server, divergev1alpha1connect.PreviewResolverServiceClient) {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergev1alpha1.AddToScheme(scheme))

	builder := clifake.NewClientBuilder().WithScheme(scheme)
	for _, env := range envs {
		builder = builder.WithObjects(env)
	}
	ctrlClient := builder.Build()

	logger := slog.Default()
	auditLogger := NewAuditLogger(logger)
	fakeK8s := fake.NewSimpleClientset()
	fakeK8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
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

	informerMgr := streaming.NewInformerManager(logger, streaming.BroadcasterMetrics{})
	pollCtx, pollCancel := context.WithCancel(context.Background())
	informerMgr.StartPolling(pollCtx, ctrlClient, time.Hour)

	cfg := TunnelManagerConfig{
		CRDClient:   ctrlClient,
		K8sClient:   fakeK8s,
		InformerMgr: informerMgr,
		Logger:      logger,
		AuditLogger: auditLogger,
	}
	if tmCfgFn != nil {
		tmCfgFn(&cfg)
	}
	tm := NewTunnelManagerWithConfig(cfg)

	mux, muxTM := NewServeMux(ServeMuxConfig{
		Client:       ctrlClient,
		K8sClient:    fakeK8s,
		InformerMgr:  informerMgr,
		Logger:       logger,
		AuditLogger:  auditLogger,
		TunnelConfig: &cfg,
	})
	require.NotNil(t, mux)
	_ = muxTM

	rpcMux := http.NewServeMux()
	rpcPath, rpcHandler := divergev1alpha1connect.NewTunnelServiceHandler(tm)
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := &auth.UserInfo{Username: "test"}
		if userFn != nil {
			user = userFn(r)
		}
		ctx := r.Context()
		if user != nil {
			ctx = auth.ContextWithUserInfo(ctx, user)
		}
		rpcHandler.ServeHTTP(w, r.WithContext(ctx))
	})
	rpcMux.Handle(rpcPath, authHandler)

	rpcServer := httptest.NewUnstartedServer(rpcMux)
	rpcServer.EnableHTTP2 = true
	rpcServer.StartTLS()

	proxySrv := NewTunnelProxyServer(tm, 0, logger)
	proxyHTTPTest := httptest.NewUnstartedServer(proxySrv.Handler)
	proxyHTTPTest.EnableHTTP2 = true
	proxyHTTPTest.StartTLS()

	t.Cleanup(func() {
		pollCancel()
		informerMgr.Close()
		rpcServer.CloseClientConnections()
		proxyHTTPTest.CloseClientConnections()
		rpcServer.Close()
		proxyHTTPTest.Close()
	})

	tunnelClient := divergev1alpha1connect.NewTunnelServiceClient(rpcServer.Client(), rpcServer.URL)
	resolveClient := divergev1alpha1connect.NewPreviewResolverServiceClient(proxyHTTPTest.Client(), proxyHTTPTest.URL)

	fx := &tunnelTestFixture{
		tm:          tm,
		rpcServer:   rpcServer,
		proxyServer: proxyHTTPTest,
		client:      tunnelClient,
		fakeK8s:     fakeK8s,
	}
	return fx, proxyHTTPTest, resolveClient
}

func TestPreviewResolver_TunnelOverridesEnvironment(t *testing.T) {
	envs := []*divergev1alpha1.Environment{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "mr-42",
				Namespace:   "apps",
				Annotations: map[string]string{"divergedev.com/created-by": "ci-bot"},
			},
			Spec: divergev1alpha1.EnvironmentSpec{
				Deploy: divergev1alpha1.EnvironmentDeploy{
					ChangedServices: []string{"web", "billing"},
				},
				ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
					ServiceName: "web",
					Port:        3000,
				},
			},
			Status: divergev1alpha1.EnvironmentStatus{
				Phase: divergev1alpha1.PhaseRunning,
			},
		},
	}

	fx, _, resolveClient := setupResolverFixture(t, envs, func(cfg *TunnelManagerConfig) {
		cfg.ProxyServiceAddr = "diverge-tunnel-proxy.diverge-system.svc:8081"
		cfg.SharedPreviewOwners = []string{"ci-bot"}
	}, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Unforced attachment of disjoint service "api" allowed via SharedPreviewOwners
	ready, _, err := dialV3TestTunnel(t, ctx, fx.client, &pb.TunnelRegister{
		Service:         "api",
		Namespace:       "apps",
		Port:            8080,
		PreviewId:       "mr-42",
		ProtocolVersion: 3,
		Metadata:        map[string]string{"kind": "service", "team": "core"},
	}, http.NotFoundHandler())
	require.NoError(t, err)

	var resp *connect.Response[pb.ResolvePreviewResponse]
	require.Eventually(t, func() bool {
		var rErr error
		resp, rErr = resolveClient.ResolvePreview(ctx, connect.NewRequest(&pb.ResolvePreviewRequest{
			PreviewId: "mr-42",
			Namespace: "apps",
		}))
		return rErr == nil && len(resp.Msg.Targets) == 3 &&
			resp.Msg.Targets[0].Mode == pb.ResolvedTargetMode_RESOLVED_TARGET_MODE_TUNNEL
	}, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, "mr-42", resp.Msg.PreviewId)
	assert.Equal(t, "apps", resp.Msg.Namespace)
	require.Len(t, resp.Msg.Targets, 3)

	// Sorted alphabetically: "api", "billing", "web"
	apiTarget := resp.Msg.Targets[0]
	assert.Equal(t, "api", apiTarget.Service)
	assert.Equal(t, "apps", apiTarget.Namespace)
	assert.Equal(t, pb.ResolvedTargetMode_RESOLVED_TARGET_MODE_TUNNEL, apiTarget.Mode)
	assert.True(t, apiTarget.Ready)
	assert.Equal(t, "http://diverge-tunnel-proxy.diverge-system.svc:8081", apiTarget.UpstreamUrl)
	assert.Equal(t, map[string]string{
		"X-Diverge-Env":       "mr-42",
		"X-Diverge-Service":   "api",
		"X-Diverge-Namespace": "apps",
	}, apiTarget.ProxyHeaders)
	assert.Equal(t, ready.TunnelId, apiTarget.TunnelId)
	assert.Equal(t, int32(8080), apiTarget.Port)
	assert.Equal(t, "test", apiTarget.Owner)
	assert.Equal(t, map[string]string{"kind": "service", "team": "core"}, apiTarget.Metadata)

	billingTarget := resp.Msg.Targets[1]
	assert.Equal(t, "billing", billingTarget.Service)
	assert.Equal(t, pb.ResolvedTargetMode_RESOLVED_TARGET_MODE_ENVIRONMENT, billingTarget.Mode)
	assert.Empty(t, billingTarget.UpstreamUrl)
	assert.Equal(t, "mr-42", billingTarget.EnvironmentName)

	webTarget := resp.Msg.Targets[2]
	assert.Equal(t, "web", webTarget.Service)
	assert.Equal(t, "apps", webTarget.Namespace)
	assert.Equal(t, pb.ResolvedTargetMode_RESOLVED_TARGET_MODE_ENVIRONMENT, webTarget.Mode)
	assert.True(t, webTarget.Ready)
	assert.Equal(t, "http://mr-42-web.apps.svc.cluster.local:3000", webTarget.UpstreamUrl)
	assert.Equal(t, "mr-42", webTarget.EnvironmentName)
	assert.Equal(t, int32(3000), webTarget.Port)
	assert.Equal(t, "ci-bot", webTarget.Owner)

	// Forced tunnels on "billing" and "web" override both ChangedServices and ServiceConfig targets.
	_, _, err = dialV3TestTunnel(t, ctx, fx.client, &pb.TunnelRegister{
		Service:         "billing",
		Namespace:       "apps",
		Port:            9090,
		PreviewId:       "mr-42",
		ProtocolVersion: 3,
		Force:           true,
	}, http.NotFoundHandler())
	require.NoError(t, err)

	_, _, err = dialV3TestTunnel(t, ctx, fx.client, &pb.TunnelRegister{
		Service:         "web",
		Namespace:       "apps",
		Port:            5173,
		PreviewId:       "mr-42",
		ProtocolVersion: 3,
		Force:           true,
	}, http.NotFoundHandler())
	require.NoError(t, err)

	var resp2 *connect.Response[pb.ResolvePreviewResponse]
	require.Eventually(t, func() bool {
		var rErr error
		resp2, rErr = resolveClient.ResolvePreview(ctx, connect.NewRequest(&pb.ResolvePreviewRequest{
			PreviewId: "mr-42",
			Namespace: "apps",
		}))
		return rErr == nil && len(resp2.Msg.Targets) == 3 &&
			resp2.Msg.Targets[1].Mode == pb.ResolvedTargetMode_RESOLVED_TARGET_MODE_TUNNEL &&
			resp2.Msg.Targets[2].Mode == pb.ResolvedTargetMode_RESOLVED_TARGET_MODE_TUNNEL
	}, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, int32(9090), resp2.Msg.Targets[1].Port)
	assert.Equal(t, int32(5173), resp2.Msg.Targets[2].Port)
}

func TestPreviewResolver_DeployNamespaceCreateAndHeaderValue(t *testing.T) {
	envCreate := &divergev1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-mr-99",
			Namespace: "apps",
		},
		Spec: divergev1alpha1.EnvironmentSpec{
			Deploy: divergev1alpha1.EnvironmentDeploy{
				Namespace: "create",
			},
			Routing: divergev1alpha1.EnvironmentRouting{
				HeaderValue: "pr-99",
			},
			ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
				ServiceName: "billing",
				Port:        9000,
			},
		},
		Status: divergev1alpha1.EnvironmentStatus{
			Phase: divergev1alpha1.PhaseRunning,
		},
	}

	_, _, resolveClient := setupResolverFixture(t, []*divergev1alpha1.Environment{envCreate}, nil, nil, nil)

	resp, err := resolveClient.ResolvePreview(context.Background(), connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "pr-99",
		Namespace: "apps",
	}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Targets, 1)
	target := resp.Msg.Targets[0]
	assert.Equal(t, "billing", target.Service)
	assert.Equal(t, "env-mr-99", target.EnvironmentName)
	assert.Equal(t, pb.ResolvedTargetMode_RESOLVED_TARGET_MODE_ENVIRONMENT, target.Mode)
	assert.Equal(t, "http://env-mr-99-billing."+envCreate.PreviewNamespace()+".svc.cluster.local:9000", target.UpstreamUrl)
	assert.Equal(t, int32(9000), target.Port)
}

func TestPreviewResolver_RemoteClusterAndEdgeCases(t *testing.T) {
	envs := []*divergev1alpha1.Environment{
		// Remote via ClusterRef -> excluded
		{
			ObjectMeta: metav1.ObjectMeta{Name: "mix-remote-ref", Namespace: "apps"},
			Spec: divergev1alpha1.EnvironmentSpec{
				Deploy:  divergev1alpha1.EnvironmentDeploy{ClusterRef: &divergev1alpha1.ClusterTargetRef{Name: "spoke-1"}},
				Routing: divergev1alpha1.EnvironmentRouting{HeaderValue: "mix-prev"},
				ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
					ServiceName: "remote-a",
					Port:        8080,
				},
			},
			Status: divergev1alpha1.EnvironmentStatus{Phase: divergev1alpha1.PhaseRunning},
		},
		// Remote via Status.TargetCluster -> excluded
		{
			ObjectMeta: metav1.ObjectMeta{Name: "mix-remote-status", Namespace: "apps"},
			Spec: divergev1alpha1.EnvironmentSpec{
				Routing: divergev1alpha1.EnvironmentRouting{HeaderValue: "mix-prev"},
				ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
					ServiceName: "remote-b",
					Port:        8080,
				},
			},
			Status: divergev1alpha1.EnvironmentStatus{
				Phase:         divergev1alpha1.PhaseRunning,
				TargetCluster: "spoke-2",
			},
		},
		// Non-Running phase -> excluded
		{
			ObjectMeta: metav1.ObjectMeta{Name: "mix-pending", Namespace: "apps"},
			Spec: divergev1alpha1.EnvironmentSpec{
				Routing: divergev1alpha1.EnvironmentRouting{HeaderValue: "mix-prev"},
				ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
					ServiceName: "pending-svc",
					Port:        8080,
				},
			},
			Status: divergev1alpha1.EnvironmentStatus{Phase: divergev1alpha1.PhasePending},
		},
		// ServiceConfig with external Endpoint set -> empty UpstreamUrl
		{
			ObjectMeta: metav1.ObjectMeta{Name: "mix-ext-endpoint", Namespace: "apps"},
			Spec: divergev1alpha1.EnvironmentSpec{
				Routing: divergev1alpha1.EnvironmentRouting{HeaderValue: "mix-prev"},
				ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
					ServiceName: "ext-svc",
					Port:        8080,
					Endpoint:    "100.64.0.1:8080",
				},
			},
			Status: divergev1alpha1.EnvironmentStatus{Phase: divergev1alpha1.PhaseRunning},
		},
		// ServiceConfig with Port == 0 -> empty UpstreamUrl
		{
			ObjectMeta: metav1.ObjectMeta{Name: "mix-zero-port", Namespace: "apps"},
			Spec: divergev1alpha1.EnvironmentSpec{
				Routing: divergev1alpha1.EnvironmentRouting{HeaderValue: "mix-prev"},
				ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
					ServiceName: "zero-port-svc",
					Port:        0,
				},
			},
			Status: divergev1alpha1.EnvironmentStatus{Phase: divergev1alpha1.PhaseRunning},
		},
		// Multi-service ChangedServices + empty ServiceConfig.ServiceName
		{
			ObjectMeta: metav1.ObjectMeta{Name: "mix-changed", Namespace: "apps"},
			Spec: divergev1alpha1.EnvironmentSpec{
				Deploy:  divergev1alpha1.EnvironmentDeploy{ChangedServices: []string{"", "orders", "billing"}},
				Routing: divergev1alpha1.EnvironmentRouting{HeaderValue: "mix-prev"},
				ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
					ServiceName: "",
					Port:        8080,
				},
			},
			Status: divergev1alpha1.EnvironmentStatus{Phase: divergev1alpha1.PhaseRunning},
		},
	}

	_, _, resolveClient := setupResolverFixture(t, envs, nil, nil, nil)

	resp, err := resolveClient.ResolvePreview(context.Background(), connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "mix-prev",
		Namespace: "apps",
	}))
	require.NoError(t, err)

	got := make(map[string]*pb.ResolvedTarget, len(resp.Msg.Targets))
	for _, tgt := range resp.Msg.Targets {
		got[tgt.Service] = tgt
	}
	assert.Len(t, got, 4)
	assert.NotContains(t, got, "")
	assert.NotContains(t, got, "remote-a")
	assert.NotContains(t, got, "remote-b")
	assert.NotContains(t, got, "pending-svc")

	for _, svc := range []string{"billing", "ext-svc", "orders", "zero-port-svc"} {
		tgt := got[svc]
		require.NotNil(t, tgt, "expected service %s", svc)
		assert.Equal(t, pb.ResolvedTargetMode_RESOLVED_TARGET_MODE_ENVIRONMENT, tgt.Mode)
		assert.True(t, tgt.Ready)
		assert.Empty(t, tgt.UpstreamUrl)
	}

	missResp, err := resolveClient.ResolvePreview(context.Background(), connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "nonexistent-prev",
		Namespace: "apps",
	}))
	require.NoError(t, err)
	assert.Empty(t, missResp.Msg.Targets)
}

func TestPreviewResolver_CodecsConnectGETAndAuth(t *testing.T) {
	envs := []*divergev1alpha1.Environment{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "mr-1", Namespace: "apps"},
			Spec: divergev1alpha1.EnvironmentSpec{
				ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
					ServiceName: "web",
					Port:        3000,
				},
			},
			Status: divergev1alpha1.EnvironmentStatus{Phase: divergev1alpha1.PhaseRunning},
		},
	}

	fx, proxySrv, unauthClient := setupResolverFixture(t, envs, func(cfg *TunnelManagerConfig) {
		cfg.AllowedNamespaces = []string{"apps"}
		cfg.DefaultNamespace = "apps"
		cfg.ProxyAuthToken = "proxy-secret-123"
		cfg.ProxyServiceAddr = "https://tunnel-proxy.apps.svc:8081"
		cfg.Directory = &stubTunnelDirectory{
			records: []TunnelRecord{{
				TunnelID:  "stub-t1",
				Namespace: "apps",
				PreviewID: "mr-1",
				Service:   "api",
				Port:      8080,
				Owner:     "alice",
				Metadata:  map[string]string{"kind": "api"},
			}},
		}
	}, nil, nil)

	// 1. Unauthenticated calls blocked with 401
	_, err := unauthClient.ResolvePreview(context.Background(), connect.NewRequest(&pb.ResolvePreviewRequest{PreviewId: "mr-1"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	reqNoToken, _ := http.NewRequest(
		http.MethodPost,
		proxySrv.URL+divergev1alpha1connect.PreviewResolverServiceResolvePreviewProcedure,
		bytes.NewBufferString(`{"previewId":"mr-1"}`),
	)
	reqNoToken.Header.Set("Content-Type", "application/json")
	respNoToken, err := proxySrv.Client().Do(reqNoToken)
	require.NoError(t, err)
	_ = respNoToken.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, respNoToken.StatusCode)

	reqWrongToken := reqNoToken.Clone(context.Background())
	reqWrongToken.Body = io.NopCloser(bytes.NewBufferString(`{"previewId":"mr-1"}`))
	reqWrongToken.Header.Set("X-Diverge-Proxy-Token", "wrong-secret")
	respWrongToken, err := proxySrv.Client().Do(reqWrongToken)
	require.NoError(t, err)
	_ = respWrongToken.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, respWrongToken.StatusCode)

	// Authorized HTTP client injecting X-Diverge-Proxy-Token
	authedHTTP := &http.Client{
		Transport: &headerInjectTransport{
			base:    proxySrv.Client().Transport,
			headers: map[string]string{"X-Diverge-Proxy-Token": "proxy-secret-123"},
		},
	}

	// 2. Binary Protobuf over POST
	protoClient := divergev1alpha1connect.NewPreviewResolverServiceClient(authedHTTP, proxySrv.URL)
	protoResp, err := protoClient.ResolvePreview(context.Background(), connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "mr-1",
	}))
	require.NoError(t, err)
	assert.Equal(t, "apps", protoResp.Msg.Namespace)
	require.Len(t, protoResp.Msg.Targets, 2)
	assert.Equal(t, "https://tunnel-proxy.apps.svc:8081", protoResp.Msg.Targets[0].UpstreamUrl)

	// 3. Connect GET client
	getClient := divergev1alpha1connect.NewPreviewResolverServiceClient(authedHTTP, proxySrv.URL, connect.WithHTTPGet())
	getResp, err := getClient.ResolvePreview(context.Background(), connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "mr-1",
	}))
	require.NoError(t, err)
	require.Len(t, getResp.Msg.Targets, 2)

	// 4. Raw Connect JSON GET query
	rawMsg := url.QueryEscape(`{"previewId":"mr-1"}`)
	rawGetURL := proxySrv.URL + divergev1alpha1connect.PreviewResolverServiceResolvePreviewProcedure + "?connect=v1&encoding=json&message=" + rawMsg
	rawGetResp, err := authedHTTP.Get(rawGetURL)
	require.NoError(t, err)
	rawGetBody, err := io.ReadAll(rawGetResp.Body)
	_ = rawGetResp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rawGetResp.StatusCode)
	var decodedGET pb.ResolvePreviewResponse
	require.NoError(t, protojson.Unmarshal(rawGetBody, &decodedGET))
	require.Len(t, decodedGET.Targets, 2)

	// 5. Raw Connect JSON POST (populated + empty targets)
	jsonReq, _ := http.NewRequest(
		http.MethodPost,
		proxySrv.URL+divergev1alpha1connect.PreviewResolverServiceResolvePreviewProcedure,
		bytes.NewBufferString(`{"previewId":"mr-1"}`),
	)
	jsonReq.Header.Set("Content-Type", "application/json")
	jsonResp, err := authedHTTP.Do(jsonReq)
	require.NoError(t, err)
	jsonBody, err := io.ReadAll(jsonResp.Body)
	_ = jsonResp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, jsonResp.StatusCode)
	var rawMap map[string]any
	require.NoError(t, json.Unmarshal(jsonBody, &rawMap))
	assert.Equal(t, "mr-1", rawMap["previewId"])

	emptyJSONReq, _ := http.NewRequest(
		http.MethodPost,
		proxySrv.URL+divergev1alpha1connect.PreviewResolverServiceResolvePreviewProcedure,
		bytes.NewBufferString(`{"previewId":"missing-preview"}`),
	)
	emptyJSONReq.Header.Set("Content-Type", "application/json")
	emptyJSONResp, err := authedHTTP.Do(emptyJSONReq)
	require.NoError(t, err)
	emptyBody, err := io.ReadAll(emptyJSONResp.Body)
	_ = emptyJSONResp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, emptyJSONResp.StatusCode)
	var decodedEmpty pb.ResolvePreviewResponse
	require.NoError(t, protojson.Unmarshal(emptyBody, &decodedEmpty))
	assert.Empty(t, decodedEmpty.Targets)

	// 6. InvalidArgument and PermissionDenied codes
	_, err = protoClient.ResolvePreview(context.Background(), connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "",
	}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = protoClient.ResolvePreview(context.Background(), connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "Invalid_Preview",
	}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = protoClient.ResolvePreview(context.Background(), connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "mr-1",
		Namespace: "Invalid_NS",
	}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = protoClient.ResolvePreview(context.Background(), connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "mr-1",
		Namespace: "kube-system",
	}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_ = fx
}

func TestTunnelService_ListTunnels_PerNamespaceSARAndFilters(t *testing.T) {
	var currentUser atomic.Value
	currentUser.Store(&auth.UserInfo{Username: "alice"})

	fx, _, _ := setupResolverFixture(t, nil, func(cfg *TunnelManagerConfig) {
		cfg.AllowedNamespaces = []string{"ns-a", "ns-b"}
	}, func(sar *authorizationv1.SubjectAccessReview) {
		if sar.Spec.ResourceAttributes != nil && sar.Spec.ResourceAttributes.Verb == "list" {
			sar.Status.Allowed = sar.Spec.ResourceAttributes.Namespace == "ns-a"
			return
		}
		sar.Status.Allowed = true
	}, func(_ *http.Request) *auth.UserInfo {
		return currentUser.Load().(*auth.UserInfo)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Establish active tunnels in ns-a (2 tunnels) and ns-b (1 tunnel)
	readyA1, _, err := dialV3TestTunnel(t, ctx, fx.client, &pb.TunnelRegister{
		Service:         "web",
		Namespace:       "ns-a",
		Port:            3000,
		PreviewId:       "prev-1",
		ProtocolVersion: 3,
		Metadata:        map[string]string{"kind": "module"},
	}, http.NotFoundHandler())
	require.NoError(t, err)

	readyA2, _, err := dialV3TestTunnel(t, ctx, fx.client, &pb.TunnelRegister{
		Service:         "api",
		Namespace:       "ns-a",
		Port:            8080,
		PreviewId:       "prev-2",
		ProtocolVersion: 3,
	}, http.NotFoundHandler())
	require.NoError(t, err)

	_, _, err = dialV3TestTunnel(t, ctx, fx.client, &pb.TunnelRegister{
		Service:         "worker",
		Namespace:       "ns-b",
		Port:            9090,
		PreviewId:       "prev-1",
		ProtocolVersion: 3,
	}, http.NotFoundHandler())
	require.NoError(t, err)

	// 1. Unauthenticated & expired rejection
	currentUser.Store((*auth.UserInfo)(nil))
	_, err = fx.client.ListTunnels(ctx, connect.NewRequest(&pb.ListTunnelsRequest{}))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	currentUser.Store(&auth.UserInfo{Username: "alice", ExpiresAt: time.Now().Add(-time.Minute)})
	_, err = fx.client.ListTunnels(ctx, connect.NewRequest(&pb.ListTunnelsRequest{}))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	// 2. Invalid DNS-1123 filter inputs -> InvalidArgument
	currentUser.Store(&auth.UserInfo{Username: "alice"})
	for _, badReq := range []*pb.ListTunnelsRequest{
		{Namespace: "Bad_NS"},
		{PreviewId: "Bad_Prev"},
		{Service: "Bad_Svc"},
	} {
		_, err = fx.client.ListTunnels(ctx, connect.NewRequest(badReq))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	}

	// 3. Explicit disallowed namespace & SAR-denied namespace -> PermissionDenied
	_, err = fx.client.ListTunnels(ctx, connect.NewRequest(&pb.ListTunnelsRequest{Namespace: "kube-system"}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_, err = fx.client.ListTunnels(ctx, connect.NewRequest(&pb.ListTunnelsRequest{Namespace: "ns-b"}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// 4. Omitted namespace returns ONLY ns-a tunnels sorted deterministically with full fields
	var listAllResp *connect.Response[pb.ListTunnelsResponse]
	require.Eventually(t, func() bool {
		var lErr error
		listAllResp, lErr = fx.client.ListTunnels(ctx, connect.NewRequest(&pb.ListTunnelsRequest{}))
		return lErr == nil && len(listAllResp.Msg.Tunnels) == 2
	}, 2*time.Second, 10*time.Millisecond)

	t1 := listAllResp.Msg.Tunnels[0]
	assert.Equal(t, readyA1.TunnelId, t1.TunnelId)
	assert.Equal(t, "ns-a", t1.Namespace)
	assert.Equal(t, "prev-1", t1.PreviewId)
	assert.Equal(t, "web", t1.Service)
	assert.Equal(t, int32(3000), t1.Port)
	assert.Equal(t, "alice", t1.Owner)
	assert.NotEmpty(t, t1.HolderPod)
	assert.Equal(t, map[string]string{"kind": "module"}, t1.Metadata)
	require.NotNil(t, t1.StartedAt)
	require.NotNil(t, t1.ExpiresAt)
	assert.True(t, t1.ExpiresAt.AsTime().After(t1.StartedAt.AsTime()))

	t2 := listAllResp.Msg.Tunnels[1]
	assert.Equal(t, readyA2.TunnelId, t2.TunnelId)
	assert.Equal(t, "prev-2", t2.PreviewId)
	assert.Equal(t, "api", t2.Service)

	// 5. Filter by preview_id and service
	filteredResp, err := fx.client.ListTunnels(ctx, connect.NewRequest(&pb.ListTunnelsRequest{
		Namespace: "ns-a",
		PreviewId: "prev-1",
		Service:   "web",
	}))
	require.NoError(t, err)
	require.Len(t, filteredResp.Msg.Tunnels, 1)
	assert.Equal(t, readyA1.TunnelId, filteredResp.Msg.Tunnels[0].TunnelId)

	// 6. Macaroon token with diverge_list_environments sees only bound namespace ns-a
	currentUser.Store(&auth.UserInfo{
		Username: "system:agenttask:ns-a:task-1",
		AuthType: "macaroon",
		Extra: map[string]authorizationv1.ExtraValue{
			auth.ExtraKeyAuthType:     {"macaroon"},
			auth.ExtraKeyNamespace:    {"ns-a"},
			auth.ExtraKeyAllowedTools: {"diverge_list_environments"},
		},
	})
	macResp, err := fx.client.ListTunnels(ctx, connect.NewRequest(&pb.ListTunnelsRequest{}))
	require.NoError(t, err)
	require.Len(t, macResp.Msg.Tunnels, 2)

	_, err = fx.client.ListTunnels(ctx, connect.NewRequest(&pb.ListTunnelsRequest{Namespace: "ns-b"}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// Macaroon without diverge_list_environments is denied on explicit ns-a and filtered to empty on omitted namespace
	currentUser.Store(&auth.UserInfo{
		Username: "system:agenttask:ns-a:task-2",
		AuthType: "macaroon",
		Extra: map[string]authorizationv1.ExtraValue{
			auth.ExtraKeyAuthType:     {"macaroon"},
			auth.ExtraKeyNamespace:    {"ns-a"},
			auth.ExtraKeyAllowedTools: {"diverge_get_environment"},
		},
	})
	_, err = fx.client.ListTunnels(ctx, connect.NewRequest(&pb.ListTunnelsRequest{Namespace: "ns-a"}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	macDeniedResp, err := fx.client.ListTunnels(ctx, connect.NewRequest(&pb.ListTunnelsRequest{}))
	require.NoError(t, err)
	assert.Empty(t, macDeniedResp.Msg.Tunnels)
}

func TestTunnelManager_PrometheusMetrics(t *testing.T) {
	tunnelsActive.Reset()
	tunnelRequestsTotal.Reset()
	resolveRequestsTotal.Reset()

	envs := []*divergev1alpha1.Environment{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "m-prev", Namespace: "ns-metrics"},
			Spec: divergev1alpha1.EnvironmentSpec{
				ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
					ServiceName: "web",
					Port:        3000,
				},
			},
			Status: divergev1alpha1.EnvironmentStatus{Phase: divergev1alpha1.PhaseRunning},
		},
	}

	fx, _, resolveClient := setupResolverFixture(t, envs, func(cfg *TunnelManagerConfig) {
		cfg.AllowedNamespaces = []string{"ns-metrics"}
	}, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, _, err := dialV3TestTunnel(t, ctx, fx.client, &pb.TunnelRegister{
		Service:          "api",
		Namespace:        "ns-metrics",
		Port:             8080,
		PreviewId:        "m-prev",
		ProtocolVersion:  3,
		ClientInstanceId: "mac-1",
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("metrics-ok"))
	}))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(tunnelsActive.WithLabelValues("ns-metrics")) == 1
	}, 2*time.Second, 10*time.Millisecond)

	// Same client_instance_id replacement keeps gauge at 1 (no leak / double-count)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	ready2, _, err := dialV3TestTunnel(t, ctx2, fx.client, &pb.TunnelRegister{
		Service:          "api",
		Namespace:        "ns-metrics",
		Port:             8080,
		PreviewId:        "m-prev",
		ProtocolVersion:  3,
		ClientInstanceId: "mac-1",
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("metrics-ok-2"))
	}))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		rec, ok := fx.tm.dir().Lookup("ns-metrics", "m-prev", "api")
		return ok && rec.TunnelID == ready2.TunnelId &&
			testutil.ToFloat64(tunnelsActive.WithLabelValues("ns-metrics")) == 1
	}, 2*time.Second, 10*time.Millisecond)

	// Proxy request metrics: valid 200 and invalid namespace -> "unknown" 400
	baseReqOK := testutil.ToFloat64(tunnelRequestsTotal.WithLabelValues("ns-metrics", "200"))
	baseReqBad := testutil.ToFloat64(tunnelRequestsTotal.WithLabelValues("unknown", "400"))

	reqOK, _ := http.NewRequest(http.MethodGet, fx.proxyServer.URL+"/ok", nil)
	reqOK.Header.Set("X-Diverge-Env", "m-prev")
	reqOK.Header.Set("X-Diverge-Namespace", "ns-metrics")
	reqOK.Header.Set("X-Diverge-Service", "api")
	respOK, err := fx.proxyServer.Client().Do(reqOK)
	require.NoError(t, err)
	_ = respOK.Body.Close()
	assert.Equal(t, http.StatusOK, respOK.StatusCode)

	reqBadNS, _ := http.NewRequest(http.MethodGet, fx.proxyServer.URL+"/bad", nil)
	reqBadNS.Header.Set("X-Diverge-Env", "m-prev")
	reqBadNS.Header.Set("X-Diverge-Namespace", "Invalid_NS")
	respBadNS, err := fx.proxyServer.Client().Do(reqBadNS)
	require.NoError(t, err)
	_ = respBadNS.Body.Close()
	assert.Equal(t, http.StatusBadRequest, respBadNS.StatusCode)

	assert.Equal(t, baseReqOK+1, testutil.ToFloat64(tunnelRequestsTotal.WithLabelValues("ns-metrics", "200")))
	assert.Equal(t, baseReqBad+1, testutil.ToFloat64(tunnelRequestsTotal.WithLabelValues("unknown", "400")))
	assert.Greater(t, testutil.CollectAndCount(tunnelRequestDuration), 0)

	// Check histogram sample count recorded in gathered Registry
	families, err := crmetrics.Registry.Gather()
	require.NoError(t, err)
	var durationSamples uint64
	for _, mf := range families {
		if mf.GetName() == "diverge_server_tunnel_request_duration_seconds" && len(mf.Metric) > 0 {
			durationSamples = mf.Metric[0].Histogram.GetSampleCount()
		}
	}
	assert.GreaterOrEqual(t, durationSamples, uint64(2))

	// ResolvePreview metrics: hit, miss, invalid_argument, permission_denied
	_, err = resolveClient.ResolvePreview(ctx2, connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "m-prev",
		Namespace: "ns-metrics",
	}))
	require.NoError(t, err)
	_, err = resolveClient.ResolvePreview(ctx2, connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "absent-prev",
		Namespace: "ns-metrics",
	}))
	require.NoError(t, err)
	_, _ = resolveClient.ResolvePreview(ctx2, connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "",
	}))
	_, _ = resolveClient.ResolvePreview(ctx2, connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "m-prev",
		Namespace: "kube-system",
	}))

	assert.Equal(t, float64(1), testutil.ToFloat64(resolveRequestsTotal.WithLabelValues("hit")))
	assert.Equal(t, float64(1), testutil.ToFloat64(resolveRequestsTotal.WithLabelValues("miss")))
	assert.Equal(t, float64(1), testutil.ToFloat64(resolveRequestsTotal.WithLabelValues("invalid_argument")))
	assert.Equal(t, float64(1), testutil.ToFloat64(resolveRequestsTotal.WithLabelValues("permission_denied")))

	// Shutdown decrements active gauge to 0
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutCancel()
	fx.tm.Shutdown(shutCtx)
	assert.Equal(t, float64(0), testutil.ToFloat64(tunnelsActive.WithLabelValues("ns-metrics")))
}

func TestPreviewResolver_HibernatedTerminatingTrimmedAndExclusiveHeaderValue(t *testing.T) {
	now := metav1.Now()
	envs := []*divergev1alpha1.Environment{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hibernated-env",
				Namespace: "apps",
				Annotations: map[string]string{
					"divergedev.com/hibernated": "true",
				},
			},
			Spec: divergev1alpha1.EnvironmentSpec{
				Routing: divergev1alpha1.EnvironmentRouting{HeaderValue: "prev-state"},
				ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
					ServiceName: "api",
					Port:        8080,
				},
			},
			Status: divergev1alpha1.EnvironmentStatus{Phase: divergev1alpha1.PhaseRunning},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "terminating-env",
				Namespace:         "apps",
				DeletionTimestamp: &now,
				Finalizers:        []string{"divergedev.com/finalizer"},
			},
			Spec: divergev1alpha1.EnvironmentSpec{
				Routing: divergev1alpha1.EnvironmentRouting{HeaderValue: "prev-state"},
				ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
					ServiceName: "gone",
					Port:        8080,
				},
			},
			Status: divergev1alpha1.EnvironmentStatus{Phase: divergev1alpha1.PhaseRunning},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "trimmed-env-raw",
				Namespace: "apps",
			},
			Spec: divergev1alpha1.EnvironmentSpec{
				Routing: divergev1alpha1.EnvironmentRouting{HeaderValue: "prev-override"},
				ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
					ServiceName: " web ",
					Port:        80,
				},
			},
			Status: divergev1alpha1.EnvironmentStatus{Phase: divergev1alpha1.PhaseRunning},
		},
	}

	_, _, resolveClient := setupResolverFixture(t, envs, nil, nil, nil)

	resp, err := resolveClient.ResolvePreview(context.Background(), connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "prev-state",
		Namespace: "apps",
	}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Targets, 1)
	assert.Equal(t, "api", resp.Msg.Targets[0].Service)
	assert.False(t, resp.Msg.Targets[0].Ready, "hibernated environment must report Ready=false")

	// When HeaderValue is set ("prev-override"), matching by env.Name ("trimmed-env-raw") must not hit.
	byNameResp, err := resolveClient.ResolvePreview(context.Background(), connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "trimmed-env-raw",
		Namespace: "apps",
	}))
	require.NoError(t, err)
	assert.Empty(t, byNameResp.Msg.Targets)

	// Matching by HeaderValue returns trimmed ServiceName and ServiceConfig Service DNS.
	byHdrResp, err := resolveClient.ResolvePreview(context.Background(), connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "prev-override",
		Namespace: "apps",
	}))
	require.NoError(t, err)
	require.Len(t, byHdrResp.Msg.Targets, 1)
	assert.Equal(t, "web", byHdrResp.Msg.Targets[0].Service)
	assert.Equal(t, "http://trimmed-env-raw-web.apps.svc.cluster.local:80", byHdrResp.Msg.Targets[0].UpstreamUrl)
}

type unfilteredStubDirectory struct {
	records []TunnelRecord
}

func (d *unfilteredStubDirectory) Lookup(namespace, previewID, service string) (*TunnelRecord, bool) {
	for i := range d.records {
		rec := d.records[i]
		if rec.Namespace == namespace && rec.PreviewID == previewID && rec.Service == service {
			return &rec, true
		}
	}
	return nil, false
}

func (d *unfilteredStubDirectory) List(_ TunnelFilter) []TunnelRecord {
	return append([]TunnelRecord(nil), d.records...)
}

func TestPreviewResolver_DefensiveDirectoryFiltering(t *testing.T) {
	dir := &unfilteredStubDirectory{
		records: []TunnelRecord{
			{TunnelID: "t-a", Namespace: "ns-a", PreviewID: "pr-1", Service: "api", Port: 8080},
			{TunnelID: "t-b", Namespace: "ns-b", PreviewID: "pr-1", Service: "api", Port: 8080},
			{TunnelID: "t-other", Namespace: "ns-a", PreviewID: "pr-2", Service: "api", Port: 8080},
		},
	}
	fx, _, resolveClient := setupResolverFixture(t, nil, func(cfg *TunnelManagerConfig) {
		cfg.Directory = dir
		cfg.ProxyServiceAddr = "diverge-tunnel-proxy.diverge-system.svc:8081"
	}, nil, nil)

	resResp, err := resolveClient.ResolvePreview(context.Background(), connect.NewRequest(&pb.ResolvePreviewRequest{
		PreviewId: "pr-1",
		Namespace: "ns-a",
	}))
	require.NoError(t, err)
	require.Len(t, resResp.Msg.Targets, 1)
	assert.Equal(t, "t-a", resResp.Msg.Targets[0].TunnelId)

	tunnelClient := divergev1alpha1connect.NewTunnelServiceClient(fx.rpcServer.Client(), fx.rpcServer.URL)
	listResp, err := tunnelClient.ListTunnels(context.Background(), connect.NewRequest(&pb.ListTunnelsRequest{
		Namespace: "ns-a",
	}))
	require.NoError(t, err)
	for _, item := range listResp.Msg.Tunnels {
		assert.Equal(t, "ns-a", item.Namespace)
	}
	assert.Len(t, listResp.Msg.Tunnels, 2)
}

func TestTunnelManager_PrometheusMetrics_UnrestrictedNSBucketsUnknownBeforeMatch(t *testing.T) {
	tunnelRequestsTotal.Reset()
	fx, _, _ := setupResolverFixture(t, nil, nil, nil, nil)

	req, _ := http.NewRequest(http.MethodGet, fx.proxyServer.URL+"/miss", nil)
	req.Header.Set("X-Diverge-Env", "absent-prev")
	req.Header.Set("X-Diverge-Namespace", "arbitrary-ns-999")
	resp, err := fx.proxyServer.Client().Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Equal(t, float64(1), testutil.ToFloat64(tunnelRequestsTotal.WithLabelValues("unknown", "502")))
	assert.Equal(t, float64(0), testutil.ToFloat64(tunnelRequestsTotal.WithLabelValues("arbitrary-ns-999", "502")))
}
