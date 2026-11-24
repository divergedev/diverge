package tunnelclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
)

type streamConnAdapter struct {
	stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]
	pr     *io.PipeReader
	pw     *io.PipeWriter
	sendMu sync.Mutex
	closed bool
	end    sync.Once
}

func newServerV3StreamConn(stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) *streamConnAdapter {
	pr, pw := io.Pipe()
	c := &streamConnAdapter{stream: stream, pr: pr, pw: pw}
	go func() {
		for {
			msg, err := stream.Receive()
			if err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			switch p := msg.Payload.(type) {
			case *pb.TunnelServiceTunnelRequest_RawData:
				if len(p.RawData) > 0 {
					if _, wErr := pw.Write(p.RawData); wErr != nil {
						return
					}
				}
			case *pb.TunnelServiceTunnelRequest_Close:
				_ = pw.CloseWithError(io.EOF)
				return
			}
		}
	}()
	return c
}

func (c *streamConnAdapter) Read(p []byte) (int, error) { return c.pr.Read(p) }
func (c *streamConnAdapter) Write(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	if err := c.stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_RawData{RawData: cp},
	}); err != nil {
		return 0, err
	}
	return len(p), nil
}
func (c *streamConnAdapter) Close() error {
	c.end.Do(func() {
		c.sendMu.Lock()
		c.closed = true
		c.sendMu.Unlock()
		_ = c.pw.CloseWithError(net.ErrClosed)
		_ = c.pr.CloseWithError(net.ErrClosed)
	})
	return nil
}
func (c *streamConnAdapter) LocalAddr() net.Addr                { return pipeAddr("srv-local") }
func (c *streamConnAdapter) RemoteAddr() net.Addr               { return pipeAddr("srv-remote") }
func (c *streamConnAdapter) SetDeadline(_ time.Time) error      { return nil }
func (c *streamConnAdapter) SetReadDeadline(_ time.Time) error  { return nil }
func (c *streamConnAdapter) SetWriteDeadline(_ time.Time) error { return nil }

type mockV3Server struct {
	divergev1alpha1connect.UnimplementedTunnelServiceHandler
	t           *testing.T
	gotReg      *pb.TunnelRegister
	mu          sync.Mutex
	proxyResp   chan string
	listTunnels *pb.ListTunnelsResponse
}

func (m *mockV3Server) Tunnel(ctx context.Context, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
	msg, err := stream.Receive()
	if err != nil {
		return err
	}
	reg := msg.GetRegister()
	m.mu.Lock()
	m.gotReg = reg
	m.mu.Unlock()

	if err := stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_Ready{
			Ready: &pb.TunnelReady{
				TunnelId:        "tun-v3-1",
				Endpoint:        "diverge-server-tunnel-proxy:8081",
				ProxyService:    "diverge-server-tunnel-proxy:8081",
				K8SServiceName:  "diverge-tunnel-abc",
				ProtocolVersion: 3,
			},
		},
	}); err != nil {
		return err
	}

	conn := newServerV3StreamConn(stream)
	defer func() { _ = conn.Close() }()

	cc, err := dialPipeHTTP2ClientConn(ctx, conn)
	if err != nil {
		return err
	}
	defer func() { _ = cc.Close() }()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://tunnel.local/hello?x=1", nil)
	if err != nil {
		return err
	}
	resp, err := cc.RoundTrip(req)
	if err != nil {
		m.proxyResp <- "err:" + err.Error()
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	m.proxyResp <- fmt.Sprintf("%d:%s", resp.StatusCode, string(body))

	<-ctx.Done()
	return nil
}

func (m *mockV3Server) ListTunnels(_ context.Context, _ *connect.Request[pb.ListTunnelsRequest]) (*connect.Response[pb.ListTunnelsResponse], error) {
	return connect.NewResponse(m.listTunnels), nil
}

func TestClient_V3EndToEndAndListTunnels(t *testing.T) {
	localSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("v3-ok:" + r.URL.RequestURI()))
	}))
	defer localSrv.Close()
	_, portStr, err := net.SplitHostPort(localSrv.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	mockSrv := &mockV3Server{
		t:         t,
		proxyResp: make(chan string, 1),
		listTunnels: &pb.ListTunnelsResponse{
			Tunnels: []*pb.TunnelInfo{
				{
					TunnelId:  "tun-v3-1",
					Namespace: "dev",
					PreviewId: "feat-1",
					Service:   "web",
					Port:      int32(port),
					Owner:     "alice",
					StartedAt: timestamppb.Now(),
				},
			},
		},
	}
	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(mockSrv)
	mux.Handle(path, handler)

	ts := httptest.NewUnstartedServer(mux)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	var readyCaptured atomic.Value
	client, err := New(Config{
		ServerAddr:       ts.URL,
		LocalHost:        "127.0.0.1",
		LocalPort:        port,
		PreviewID:        "feat-1",
		Service:          "web",
		Namespace:        "dev",
		Metadata:         map[string]string{"kind": "module"},
		Force:            true,
		SkipK8sService:   true,
		ClientInstanceID: "client-uuid-1",
		TunnelHTTPClient: ts.Client(),
		Events: EventHandlers{
			OnReady: func(info ReadyInfo) { readyCaptured.Store(info) },
		},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Connect(ctx) }()

	select {
	case res := <-mockSrv.proxyResp:
		assert.Equal(t, "201:v3-ok:/hello?x=1", res)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for v3 proxied response")
	}

	<-client.Ready
	gotReady, ok := readyCaptured.Load().(ReadyInfo)
	require.True(t, ok)
	assert.Equal(t, int32(3), gotReady.ProtocolVersion)
	assert.Equal(t, "diverge-server-tunnel-proxy:8081", gotReady.ProxyService)
	assert.Equal(t, "diverge-tunnel-abc", gotReady.K8sServiceName)

	mockSrv.mu.Lock()
	require.NotNil(t, mockSrv.gotReg)
	assert.Equal(t, int32(3), mockSrv.gotReg.ProtocolVersion)
	assert.Equal(t, "client-uuid-1", mockSrv.gotReg.ClientInstanceId)
	assert.True(t, mockSrv.gotReg.Force)
	assert.True(t, mockSrv.gotReg.SkipK8SService)
	assert.Equal(t, "module", mockSrv.gotReg.Metadata["kind"])
	mockSrv.mu.Unlock()

	listResp, err := ListTunnels(ctx, ts.Client(), ts.URL, &pb.ListTunnelsRequest{Namespace: "dev"})
	require.NoError(t, err)
	require.Len(t, listResp.Tunnels, 1)
	assert.Equal(t, "tun-v3-1", listResp.Tunnels[0].TunnelId)

	cancel()
	require.NoError(t, <-done)
}

type mockV2FallbackServer struct {
	divergev1alpha1connect.UnimplementedTunnelServiceHandler
	respCh chan *pb.TunnelHTTPResponse
}

func (m *mockV2FallbackServer) Tunnel(ctx context.Context, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
	if _, err := stream.Receive(); err != nil {
		return err
	}
	if err := stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_Ready{
			Ready: &pb.TunnelReady{TunnelId: "tun-v2-legacy", ProtocolVersion: 0},
		},
	}); err != nil {
		return err
	}

	go func() {
		for {
			msg, err := stream.Receive()
			if err != nil {
				return
			}
			if r := msg.GetHttpResponse(); r != nil {
				m.respCh <- r
			}
		}
	}()

	// Valid path + SSRF attempt path.
	_ = stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_HttpRequest{
			HttpRequest: &pb.TunnelHTTPRequest{RequestId: "r-ok", Method: "GET", Path: "/v2"},
		},
	})
	_ = stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_HttpRequest{
			HttpRequest: &pb.TunnelHTTPRequest{RequestId: "r-ssrf", Method: "GET", Path: "http://169.254.169.254/latest"},
		},
	})

	<-ctx.Done()
	return nil
}

func TestClient_V2FallbackAndPathValidation(t *testing.T) {
	localSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example.com", http.StatusFound)
	}))
	defer localSrv.Close()
	_, portStr, err := net.SplitHostPort(localSrv.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	mockSrv := &mockV2FallbackServer{respCh: make(chan *pb.TunnelHTTPResponse, 4)}
	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(mockSrv)
	mux.Handle(path, handler)

	ts := httptest.NewUnstartedServer(mux)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	client, err := New(Config{
		ServerAddr:       ts.URL,
		LocalHost:        "127.0.0.1",
		LocalPort:        port,
		PreviewID:        "feat-1",
		Service:          "web",
		Namespace:        "dev",
		TunnelHTTPClient: ts.Client(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = client.Connect(ctx) }()

	<-client.Ready
	assert.Equal(t, int32(2), client.ReadyInfo().ProtocolVersion)

	got := map[string]*pb.TunnelHTTPResponse{}
	for len(got) < 2 {
		select {
		case r := <-mockSrv.respCh:
			got[r.RequestId] = r
		case <-time.After(3 * time.Second):
			t.Fatal("timeout waiting for v2 responses")
		}
	}
	assert.Equal(t, int32(http.StatusFound), got["r-ok"].StatusCode)
	assert.Equal(t, int32(http.StatusBadRequest), got["r-ssrf"].StatusCode)
}

type fakeInvalidatableSource struct {
	mu          sync.Mutex
	calls       int
	invalidated int
	tokens      []string
}

func (f *fakeInvalidatableSource) Token(_ context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := f.calls
	if idx >= len(f.tokens) {
		idx = len(f.tokens) - 1
	}
	f.calls++
	return f.tokens[idx], nil
}

func (f *fakeInvalidatableSource) Invalidate() {
	f.mu.Lock()
	f.invalidated++
	f.mu.Unlock()
}

type retryScenarioServer struct {
	divergev1alpha1connect.UnimplementedTunnelServiceHandler
	mu       sync.Mutex
	attempts int
	stepFn   func(attempt int, authHeader string, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error
}

func (s *retryScenarioServer) Tunnel(ctx context.Context, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
	s.mu.Lock()
	s.attempts++
	attempt := s.attempts
	s.mu.Unlock()
	return s.stepFn(attempt, stream.RequestHeader().Get("Authorization"), stream)
}

func TestClient_ConnectWithRetry_Matrix(t *testing.T) {
	startRetryServer := func(t *testing.T, fn func(attempt int, auth string, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error) (*retryScenarioServer, string, *http.Client) {
		t.Helper()
		srv := &retryScenarioServer{stepFn: fn}
		mux := http.NewServeMux()
		path, handler := divergev1alpha1connect.NewTunnelServiceHandler(srv)
		mux.Handle(path, handler)
		ts := httptest.NewUnstartedServer(mux)
		ts.EnableHTTP2 = true
		ts.StartTLS()
		t.Cleanup(ts.Close)
		return srv, ts.URL, ts.Client()
	}

	cases := []struct {
		name         string
		tokens       []string
		step         func(attempt int, auth string, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse], cancel context.CancelFunc) error
		wantAttempts int
		wantInv      int
		wantErr      string
	}{
		{
			name:   "reconnects on CLOSE_CODE_SERVER_SHUTDOWN",
			tokens: []string{"t1"},
			step: func(attempt int, _ string, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse], cancel context.CancelFunc) error {
				_, _ = stream.Receive()
				_ = stream.Send(&pb.TunnelServiceTunnelResponse{Payload: &pb.TunnelServiceTunnelResponse_Ready{Ready: &pb.TunnelReady{TunnelId: "t1", ProtocolVersion: 3}}})
				if attempt == 1 {
					_ = stream.Send(&pb.TunnelServiceTunnelResponse{Payload: &pb.TunnelServiceTunnelResponse_Close{Close: &pb.TunnelClose{Code: pb.CloseCode_CLOSE_CODE_SERVER_SHUTDOWN, Reason: "drain"}}})
					return nil
				}
				cancel()
				return nil
			},
			wantAttempts: 2,
		},
		{
			name:   "reconnects on CLOSE_CODE_HEARTBEAT_TIMEOUT",
			tokens: []string{"t1"},
			step: func(attempt int, _ string, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse], cancel context.CancelFunc) error {
				_, _ = stream.Receive()
				_ = stream.Send(&pb.TunnelServiceTunnelResponse{Payload: &pb.TunnelServiceTunnelResponse_Ready{Ready: &pb.TunnelReady{TunnelId: "t1", ProtocolVersion: 3}}})
				if attempt == 1 {
					_ = stream.Send(&pb.TunnelServiceTunnelResponse{Payload: &pb.TunnelServiceTunnelResponse_Close{Close: &pb.TunnelClose{Code: pb.CloseCode_CLOSE_CODE_HEARTBEAT_TIMEOUT, Reason: "missed ping"}}})
					return nil
				}
				cancel()
				return nil
			},
			wantAttempts: 2,
		},
		{
			name:   "exits without retry on CLOSE_CODE_EVICTED",
			tokens: []string{"t1"},
			step: func(_ int, _ string, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse], _ context.CancelFunc) error {
				_, _ = stream.Receive()
				_ = stream.Send(&pb.TunnelServiceTunnelResponse{Payload: &pb.TunnelServiceTunnelResponse_Ready{Ready: &pb.TunnelReady{TunnelId: "t1", ProtocolVersion: 3}}})
				_ = stream.Send(&pb.TunnelServiceTunnelResponse{Payload: &pb.TunnelServiceTunnelResponse_Close{Close: &pb.TunnelClose{Code: pb.CloseCode_CLOSE_CODE_EVICTED, Reason: "taken over"}}})
				return nil
			},
			wantAttempts: 1,
			wantErr:      "CLOSE_CODE_EVICTED",
		},
		{
			name:   "exits without retry on CodeAlreadyExists",
			tokens: []string{"t1"},
			step: func(_ int, _ string, _ *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse], _ context.CancelFunc) error {
				return connect.NewError(connect.CodeAlreadyExists, errors.New("conflict"))
			},
			wantAttempts: 1,
			wantErr:      "already_exists",
		},
		{
			name:   "exits without retry on CodePermissionDenied",
			tokens: []string{"t1"},
			step: func(_ int, _ string, _ *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse], _ context.CancelFunc) error {
				return connect.NewError(connect.CodePermissionDenied, errors.New("forbidden"))
			},
			wantAttempts: 1,
			wantErr:      "permission_denied",
		},
		{
			name:   "exits without retry on CodeInvalidArgument",
			tokens: []string{"t1"},
			step: func(_ int, _ string, _ *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse], _ context.CancelFunc) error {
				return connect.NewError(connect.CodeInvalidArgument, errors.New("bad arg"))
			},
			wantAttempts: 1,
			wantErr:      "invalid_argument",
		},
		{
			name:   "CodeUnauthenticated invalidates once and succeeds with fresh token",
			tokens: []string{"stale", "fresh"},
			step: func(attempt int, auth string, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse], cancel context.CancelFunc) error {
				if auth != "Bearer fresh" {
					return connect.NewError(connect.CodeUnauthenticated, errors.New("expired"))
				}
				_, _ = stream.Receive()
				_ = stream.Send(&pb.TunnelServiceTunnelResponse{Payload: &pb.TunnelServiceTunnelResponse_Ready{Ready: &pb.TunnelReady{TunnelId: "t1", ProtocolVersion: 3}}})
				cancel()
				return nil
			},
			wantAttempts: 2,
			wantInv:      1,
		},
		{
			name:   "CodeUnauthenticated exits after single failed refresh retry",
			tokens: []string{"stale", "still-bad"},
			step: func(_ int, _ string, _ *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse], _ context.CancelFunc) error {
				return connect.NewError(connect.CodeUnauthenticated, errors.New("still invalid"))
			},
			wantAttempts: 2,
			wantInv:      1,
			wantErr:      "unauthenticated",
		},
		{
			name:   "CLOSE_CODE_AUTH_EXPIRED invalidates once then stops on immediate repeat",
			tokens: []string{"tok1", "tok2"},
			step: func(_ int, _ string, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse], _ context.CancelFunc) error {
				_, _ = stream.Receive()
				_ = stream.Send(&pb.TunnelServiceTunnelResponse{Payload: &pb.TunnelServiceTunnelResponse_Ready{Ready: &pb.TunnelReady{TunnelId: "t1", ProtocolVersion: 3}}})
				_ = stream.Send(&pb.TunnelServiceTunnelResponse{Payload: &pb.TunnelServiceTunnelResponse_Close{Close: &pb.TunnelClose{Code: pb.CloseCode_CLOSE_CODE_AUTH_EXPIRED, Reason: "expired"}}})
				return nil
			},
			wantAttempts: 2,
			wantInv:      1,
			wantErr:      "CLOSE_CODE_AUTH_EXPIRED",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			srv, urlStr, httpCli := startRetryServer(t, func(attempt int, auth string, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
				return tc.step(attempt, auth, stream, cancel)
			})
			tokSrc := &fakeInvalidatableSource{tokens: tc.tokens}
			authedCli := &http.Client{
				Transport: &AuthTransport{
					Base:        httpCli.Transport,
					TokenSource: tokSrc,
				},
			}

			client, err := New(Config{
				ServerAddr:       urlStr,
				LocalHost:        "127.0.0.1",
				LocalPort:        8080,
				PreviewID:        "p1",
				Service:          "web",
				Namespace:        "dev",
				TokenSource:      tokSrc,
				TunnelHTTPClient: authedCli,
				InitialBackoff:   10 * time.Millisecond,
				MaxBackoff:       30 * time.Millisecond,
			})
			require.NoError(t, err)

			err = client.ConnectWithRetry(ctx)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
			}

			srv.mu.Lock()
			assert.Equal(t, tc.wantAttempts, srv.attempts)
			srv.mu.Unlock()

			tokSrc.mu.Lock()
			assert.Equal(t, tc.wantInv, tokSrc.invalidated)
			tokSrc.mu.Unlock()
		})
	}
}

func TestNew_RejectsPortInLocalHostAndInsecureClassifyDisconnect(t *testing.T) {
	_, err := New(Config{
		ServerAddr: "https://diverge.example.com",
		LocalHost:  "127.0.0.1:9999",
		LocalPort:  8080,
		PreviewID:  "feat-1",
		Service:    "web",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a loopback address")

	c, err := New(Config{
		ServerAddr: "https://diverge.example.com",
		LocalHost:  "127.0.0.1",
		LocalPort:  8080,
		PreviewID:  "feat-1",
		Service:    "web",
	})
	require.NoError(t, err)
	assert.Zero(t, c.localClient().Timeout, "default v2 local HTTP client must not cap healthy long transfers")
	localTr, ok := c.localClient().Transport.(*http.Transport)
	require.True(t, ok)
	assert.Equal(t, localRespHeaderTimeout, localTr.ResponseHeaderTimeout)

	authRetried := false
	_, retry, outErr := c.classifyDisconnect(fmt.Errorf("dial: %w", ErrInsecureHTTP), time.Second, &authRetried)
	assert.False(t, retry)
	assert.ErrorIs(t, outErr, ErrInsecureHTTP)
}

type mockV3CloseServer struct {
	divergev1alpha1connect.UnimplementedTunnelServiceHandler
	closeCh chan *pb.TunnelClose
}

func (m *mockV3CloseServer) Tunnel(_ context.Context, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
	if _, err := stream.Receive(); err != nil {
		return err
	}
	if err := stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_Ready{
			Ready: &pb.TunnelReady{
				TunnelId:        "tun-close-1",
				Endpoint:        "diverge-server-tunnel-proxy:8081",
				ProxyService:    "diverge-server-tunnel-proxy:8081",
				K8SServiceName:  "diverge-tunnel-close",
				ProtocolVersion: 3,
			},
		},
	}); err != nil {
		return err
	}
	for {
		msg, err := stream.Receive()
		if err != nil {
			return nil
		}
		if cls := msg.GetClose(); cls != nil {
			m.closeCh <- cls
			return nil
		}
	}
}

func TestClient_ConnectCancelSendsTunnelClose(t *testing.T) {
	mockSrv := &mockV3CloseServer{
		closeCh: make(chan *pb.TunnelClose, 1),
	}
	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(mockSrv)
	mux.Handle(path, handler)

	ts := httptest.NewUnstartedServer(mux)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	c, err := New(Config{
		ServerAddr:       ts.URL,
		LocalHost:        "127.0.0.1",
		LocalPort:        8080,
		PreviewID:        "feat-close",
		Service:          "web",
		Namespace:        "dev",
		TunnelHTTPClient: ts.Client(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Connect(ctx) }()

	<-c.Ready
	cancel()

	require.NoError(t, <-done)
	select {
	case cls := <-mockSrv.closeCh:
		assert.Equal(t, pb.CloseCode_CLOSE_CODE_UNSPECIFIED, cls.GetCode())
		assert.Equal(t, "client shutting down", cls.GetReason())
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for TunnelClose frame on client shutdown")
	}
}

func TestClient_ConnectWithRetry_AuthRetryBudgetUsesReadyDuration(t *testing.T) {
	startRetryServer := func(t *testing.T, fn func(attempt int, auth string, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error) (*retryScenarioServer, string, *http.Client) {
		t.Helper()
		srv := &retryScenarioServer{stepFn: fn}
		mux := http.NewServeMux()
		path, handler := divergev1alpha1connect.NewTunnelServiceHandler(srv)
		mux.Handle(path, handler)
		ts := httptest.NewUnstartedServer(mux)
		ts.EnableHTTP2 = true
		ts.StartTLS()
		t.Cleanup(ts.Close)
		return srv, ts.URL, ts.Client()
	}

	t.Run("slow pre-ready auth rejection does not reset retry budget", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		srv, urlStr, httpCli := startRetryServer(t, func(_ int, _ string, _ *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
			time.Sleep(70 * time.Millisecond)
			return connect.NewError(connect.CodeUnauthenticated, errors.New("still invalid"))
		})
		tokSrc := &fakeInvalidatableSource{tokens: []string{"t1", "t2"}}
		authedCli := &http.Client{
			Transport: &AuthTransport{Base: httpCli.Transport, TokenSource: tokSrc},
		}
		client, err := New(Config{
			ServerAddr:       urlStr,
			LocalHost:        "127.0.0.1",
			LocalPort:        8080,
			PreviewID:        "p1",
			Service:          "web",
			Namespace:        "dev",
			TokenSource:      tokSrc,
			TunnelHTTPClient: authedCli,
			InitialBackoff:   5 * time.Millisecond,
			MaxBackoff:       10 * time.Millisecond,
		})
		require.NoError(t, err)
		client.authRetryWindow = 40 * time.Millisecond

		err = client.ConnectWithRetry(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unauthenticated")

		srv.mu.Lock()
		assert.Equal(t, 2, srv.attempts)
		srv.mu.Unlock()
		tokSrc.mu.Lock()
		assert.Equal(t, 1, tokSrc.invalidated)
		tokSrc.mu.Unlock()
	})

	t.Run("stable ready session resets auth retry budget", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		readyCh := make(chan struct{}, 3)
		srv, urlStr, httpCli := startRetryServer(t, func(attempt int, _ string, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
			_, _ = stream.Receive()
			_ = stream.Send(&pb.TunnelServiceTunnelResponse{Payload: &pb.TunnelServiceTunnelResponse_Ready{Ready: &pb.TunnelReady{TunnelId: "t1", ProtocolVersion: 3}}})
			if attempt < 3 {
				select {
				case <-readyCh:
				case <-ctx.Done():
					return nil
				}
				time.Sleep(60 * time.Millisecond)
				_ = stream.Send(&pb.TunnelServiceTunnelResponse{Payload: &pb.TunnelServiceTunnelResponse_Close{Close: &pb.TunnelClose{Code: pb.CloseCode_CLOSE_CODE_AUTH_EXPIRED, Reason: "expired"}}})
				return nil
			}
			cancel()
			return nil
		})
		tokSrc := &fakeInvalidatableSource{tokens: []string{"t1", "t2", "t3"}}
		authedCli := &http.Client{
			Transport: &AuthTransport{Base: httpCli.Transport, TokenSource: tokSrc},
		}
		client, err := New(Config{
			ServerAddr:       urlStr,
			LocalHost:        "127.0.0.1",
			LocalPort:        8080,
			PreviewID:        "p1",
			Service:          "web",
			Namespace:        "dev",
			TokenSource:      tokSrc,
			TunnelHTTPClient: authedCli,
			InitialBackoff:   5 * time.Millisecond,
			MaxBackoff:       10 * time.Millisecond,
			Events: EventHandlers{
				OnReady: func(ReadyInfo) {
					readyCh <- struct{}{}
				},
			},
		})
		require.NoError(t, err)
		client.authRetryWindow = 40 * time.Millisecond

		err = client.ConnectWithRetry(ctx)
		require.NoError(t, err)

		srv.mu.Lock()
		assert.Equal(t, 3, srv.attempts)
		srv.mu.Unlock()
		tokSrc.mu.Lock()
		assert.Equal(t, 2, tokSrc.invalidated)
		tokSrc.mu.Unlock()
	})

	t.Run("slow OnClosed callback does not count toward stable auth window", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		srv, urlStr, httpCli := startRetryServer(t, func(_ int, _ string, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
			_, _ = stream.Receive()
			_ = stream.Send(&pb.TunnelServiceTunnelResponse{Payload: &pb.TunnelServiceTunnelResponse_Ready{Ready: &pb.TunnelReady{TunnelId: "t1", ProtocolVersion: 3}}})
			_ = stream.Send(&pb.TunnelServiceTunnelResponse{Payload: &pb.TunnelServiceTunnelResponse_Close{Close: &pb.TunnelClose{Code: pb.CloseCode_CLOSE_CODE_AUTH_EXPIRED, Reason: "expired"}}})
			return nil
		})
		tokSrc := &fakeInvalidatableSource{tokens: []string{"t1", "t2"}}
		authedCli := &http.Client{
			Transport: &AuthTransport{Base: httpCli.Transport, TokenSource: tokSrc},
		}
		client, err := New(Config{
			ServerAddr:       urlStr,
			LocalHost:        "127.0.0.1",
			LocalPort:        8080,
			PreviewID:        "p1",
			Service:          "web",
			Namespace:        "dev",
			TokenSource:      tokSrc,
			TunnelHTTPClient: authedCli,
			InitialBackoff:   5 * time.Millisecond,
			MaxBackoff:       10 * time.Millisecond,
			Events: EventHandlers{
				OnClosed: func(pb.CloseCode, string) {
					time.Sleep(70 * time.Millisecond)
				},
			},
		})
		require.NoError(t, err)
		client.authRetryWindow = 40 * time.Millisecond

		err = client.ConnectWithRetry(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CLOSE_CODE_AUTH_EXPIRED")

		srv.mu.Lock()
		assert.Equal(t, 2, srv.attempts)
		srv.mu.Unlock()
		tokSrc.mu.Lock()
		assert.Equal(t, 1, tokSrc.invalidated)
		tokSrc.mu.Unlock()
	})

	t.Run("register Send EOF still classifies RPC error from Receive", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		var attempts atomic.Int32
		rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts.Add(1)
			if req.Body != nil {
				_ = req.Body.Close()
			}
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Status:     "403 Forbidden",
				ProtoMajor: 2,
				ProtoMinor: 0,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body:    http.NoBody,
				Request: req,
			}, nil
		})
		client, err := New(Config{
			ServerAddr:       "https://diverge.example.com",
			LocalHost:        "127.0.0.1",
			LocalPort:        8080,
			PreviewID:        "p1",
			Service:          "web",
			Namespace:        "dev",
			TunnelHTTPClient: &http.Client{Transport: rt},
			InitialBackoff:   5 * time.Millisecond,
			MaxBackoff:       10 * time.Millisecond,
		})
		require.NoError(t, err)

		err = client.ConnectWithRetry(ctx)
		require.Error(t, err)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		assert.Equal(t, int32(1), attempts.Load())
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type mockV2BackpressureServer struct {
	divergev1alpha1connect.UnimplementedTunnelServiceHandler
	release chan struct{}
}

func (m *mockV2BackpressureServer) Tunnel(_ context.Context, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
	if _, err := stream.Receive(); err != nil {
		return err
	}
	if err := stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_Ready{
			Ready: &pb.TunnelReady{TunnelId: "tun-v2-bp", ProtocolVersion: 2},
		},
	}); err != nil {
		return err
	}
	if err := stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_HttpRequest{
			HttpRequest: &pb.TunnelHTTPRequest{RequestId: "req-bp", Method: "GET", Path: "/big"},
		},
	}); err != nil {
		return err
	}
	// Stop calling stream.Receive() so outer HTTP/2 flow-control fills and blocks handleV2HTTPRequest on sendMu.
	<-m.release
	return nil
}

func waitForBackpressure(t *testing.T, bytesWritten *atomic.Int64) {
	t.Helper()
	require.Eventually(t, func() bool {
		return bytesWritten.Load() >= 256<<10
	}, 5*time.Second, 10*time.Millisecond)
	var prev int64 = -1
	require.Eventually(t, func() bool {
		cur := bytesWritten.Load()
		stalled := cur == prev
		prev = cur
		return stalled
	}, 3*time.Second, 50*time.Millisecond)
}

func TestClient_V2ShutdownBoundedUnderBackpressure(t *testing.T) {
	var bytesWritten atomic.Int64
	localSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 64<<10)
		for {
			n, err := w.Write(chunk)
			if n > 0 {
				bytesWritten.Add(int64(n))
			}
			if flusher != nil {
				flusher.Flush()
			}
			if err != nil {
				return
			}
			select {
			case <-r.Context().Done():
				return
			default:
			}
		}
	}))
	t.Cleanup(localSrv.Close)
	t.Cleanup(localSrv.CloseClientConnections)
	_, portStr, err := net.SplitHostPort(localSrv.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	release := make(chan struct{})
	var releaseOnce sync.Once
	closeRelease := func() { releaseOnce.Do(func() { close(release) }) }
	srv := &mockV2BackpressureServer{release: release}
	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(srv)
	mux.Handle(path, handler)

	ts := httptest.NewUnstartedServer(mux)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	t.Cleanup(ts.Close)
	t.Cleanup(closeRelease)

	c, err := New(Config{
		ServerAddr:       ts.URL,
		LocalHost:        "127.0.0.1",
		LocalPort:        port,
		PreviewID:        "feat-v2-bp",
		Service:          "web",
		Namespace:        "dev",
		TunnelHTTPClient: ts.Client(),
	})
	require.NoError(t, err)
	c.shutdownGracePeriod = 100 * time.Millisecond
	c.streamCloseTimeout = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- c.Connect(ctx) }()

	<-c.Ready
	waitForBackpressure(t, &bytesWritten)

	start := time.Now()
	cancel()
	select {
	case connectErr := <-done:
		require.NoError(t, connectErr)
	case <-time.After(1 * time.Second):
		t.Fatal("v2 Connect(ctx) hung on shutdown under backpressure")
	}
	assert.Less(t, time.Since(start), 1*time.Second)
}

type mockV3BackpressureServer struct {
	divergev1alpha1connect.UnimplementedTunnelServiceHandler
	release chan struct{}
}

func (m *mockV3BackpressureServer) Tunnel(ctx context.Context, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
	if _, err := stream.Receive(); err != nil {
		return err
	}
	if err := stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_Ready{
			Ready: &pb.TunnelReady{TunnelId: "tun-v3-bp", ProtocolVersion: 3},
		},
	}); err != nil {
		return err
	}

	var pauseRecv atomic.Bool
	pr, pw := io.Pipe()
	conn := &streamConnAdapter{stream: stream, pr: pr, pw: pw}
	defer func() { _ = conn.Close() }()

	go func() {
		for {
			if pauseRecv.Load() {
				<-m.release
				_ = pw.CloseWithError(io.EOF)
				return
			}
			msg, rErr := stream.Receive()
			if rErr != nil {
				_ = pw.CloseWithError(rErr)
				return
			}
			switch p := msg.Payload.(type) {
			case *pb.TunnelServiceTunnelRequest_RawData:
				if len(p.RawData) > 0 && !pauseRecv.Load() {
					_, _ = pw.Write(p.RawData)
				}
			case *pb.TunnelServiceTunnelRequest_Close:
				_ = pw.CloseWithError(io.EOF)
				return
			}
		}
	}()

	cc, err := dialPipeHTTP2ClientConn(ctx, conn)
	if err != nil {
		return err
	}
	defer func() { _ = cc.Close() }()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://tunnel.local/big", nil)
	if err != nil {
		return err
	}
	resp, err := cc.RoundTrip(req)
	if err != nil {
		return err
	}
	// Hold resp.Body unread and stop draining stream.Receive() so outer Connect HTTP/2 flow control fills.
	pauseRecv.Store(true)
	<-m.release
	_ = resp.Body.Close()
	return nil
}

func TestClient_V3ShutdownBoundedUnderBackpressure(t *testing.T) {
	var bytesWritten atomic.Int64
	localSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 64<<10)
		for {
			n, err := w.Write(chunk)
			if n > 0 {
				bytesWritten.Add(int64(n))
			}
			if flusher != nil {
				flusher.Flush()
			}
			if err != nil {
				return
			}
			select {
			case <-r.Context().Done():
				return
			default:
			}
		}
	}))
	t.Cleanup(localSrv.Close)
	t.Cleanup(localSrv.CloseClientConnections)
	_, portStr, err := net.SplitHostPort(localSrv.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	release := make(chan struct{})
	var releaseOnce sync.Once
	closeRelease := func() { releaseOnce.Do(func() { close(release) }) }
	srv := &mockV3BackpressureServer{release: release}
	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(srv)
	mux.Handle(path, handler)

	ts := httptest.NewUnstartedServer(mux)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	t.Cleanup(ts.Close)
	t.Cleanup(closeRelease)

	c, err := New(Config{
		ServerAddr:       ts.URL,
		LocalHost:        "127.0.0.1",
		LocalPort:        port,
		PreviewID:        "feat-v3-bp",
		Service:          "web",
		Namespace:        "dev",
		TunnelHTTPClient: ts.Client(),
	})
	require.NoError(t, err)
	c.shutdownGracePeriod = 100 * time.Millisecond
	c.streamCloseTimeout = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- c.Connect(ctx) }()

	<-c.Ready
	waitForBackpressure(t, &bytesWritten)

	start := time.Now()
	cancel()
	select {
	case connectErr := <-done:
		require.NoError(t, connectErr)
	case <-time.After(1 * time.Second):
		t.Fatal("v3 Connect(ctx) hung on shutdown under backpressure")
	}
	assert.Less(t, time.Since(start), 1*time.Second)
}
