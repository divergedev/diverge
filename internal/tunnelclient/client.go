package tunnelclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
)

const (
	// ProtocolVersion is the highest tunnel protocol version supported by this client.
	ProtocolVersion = 3

	v2ChunkSize           = 32 << 10 // 32 KiB
	defaultInitialBackoff = 1 * time.Second
	defaultMaxBackoff     = 30 * time.Second
	authRetryStableWindow = 5 * time.Second
	shutdownGraceTimeout  = 2 * time.Second
	streamWaitDoneTimeout = 1 * time.Second
)

// ReadyInfo captures the active tunnel registration details returned by TunnelReady.
type ReadyInfo struct {
	TunnelID        string
	Endpoint        string
	ProxyService    string
	K8sServiceName  string
	ProtocolVersion int32
}

// EventHandlers receives optional lifecycle notifications from Client without
// writing directly to stdout or stderr.
type EventHandlers struct {
	OnReady        func(info ReadyInfo)
	OnReconnecting func(err error, backoff time.Duration)
	OnClosed       func(code pb.CloseCode, reason string)
	OnError        func(err error)
}

// Config configures a standalone tunnel Client.
type Config struct {
	ServerAddr       string
	LocalHost        string
	LocalPort        int
	PreviewID        string
	Service          string
	Namespace        string
	Metadata         map[string]string
	Force            bool
	SkipK8sService   bool
	ClientInstanceID string
	TokenSource      TokenSource
	BaseTransport    http.RoundTripper
	TunnelHTTPClient *http.Client
	LocalHTTPClient  *http.Client
	LocalTransport   http.RoundTripper
	DialRetryTimeout time.Duration
	InitialBackoff   time.Duration
	MaxBackoff       time.Duration
	Logger           *slog.Logger
	Events           EventHandlers
}

// Client manages a single ConnectRPC reverse tunnel session and reconnect loop.
type Client struct {
	// Ready is closed once when the first TunnelReady message is received.
	Ready chan struct{}

	cfg                 Config
	logger              *slog.Logger
	readyOnce           sync.Once
	mu                  sync.RWMutex
	readyInfo           ReadyInfo
	tunnelHTTPClient    *http.Client
	localHTTPClient     *http.Client
	authRetryWindow     time.Duration
	shutdownGracePeriod time.Duration
	streamCloseTimeout  time.Duration
}

// New validates cfg and constructs a Client.
func New(cfg Config) (*Client, error) {
	cfg.LocalHost = strings.TrimSpace(cfg.LocalHost)
	if cfg.LocalHost == "" {
		cfg.LocalHost = "127.0.0.1"
	}
	if !IsLoopbackAddress(cfg.LocalHost) {
		return nil, fmt.Errorf("local host %q must be a loopback address", cfg.LocalHost)
	}
	if cfg.LocalPort < 1 || cfg.LocalPort > 65535 {
		return nil, fmt.Errorf("local port %d out of valid range 1-65535", cfg.LocalPort)
	}
	if cfg.DialRetryTimeout == 0 {
		cfg.DialRetryTimeout = defaultDialRetryTimeout
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = defaultInitialBackoff
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = defaultMaxBackoff
	}
	if cfg.MaxBackoff < cfg.InitialBackoff {
		cfg.MaxBackoff = cfg.InitialBackoff
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	tunnelHTTP := cfg.TunnelHTTPClient
	if tunnelHTTP == nil {
		tunnelHTTP = NewHTTPClient(cfg.ServerAddr, cfg.TokenSource, cfg.BaseTransport)
	}
	localHTTP := cfg.LocalHTTPClient
	if localHTTP == nil {
		localHTTP = &http.Client{
			Transport: newRetryingLoopbackTransport(0),
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	return &Client{
		Ready:            make(chan struct{}),
		cfg:              cfg,
		logger:           logger,
		tunnelHTTPClient: tunnelHTTP,
		localHTTPClient:  localHTTP,
	}, nil
}

// ReadyInfo returns the most recently negotiated TunnelReady metadata.
func (c *Client) ReadyInfo() ReadyInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.readyInfo
}

// TunnelHTTPClient returns the HTTP client used for control-plane RPCs.
func (c *Client) TunnelHTTPClient() *http.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tunnelHTTPClient
}

// SetTunnelHTTPClient overrides the HTTP client used for tunnel RPCs (intended for tests).
func (c *Client) SetTunnelHTTPClient(client *http.Client) {
	if client == nil {
		return
	}
	c.mu.Lock()
	c.tunnelHTTPClient = client
	c.mu.Unlock()
}

// SetLocalHTTPClient overrides the HTTP client used by Protocol v2 local requests (intended for tests).
func (c *Client) SetLocalHTTPClient(client *http.Client) {
	if client == nil {
		return
	}
	c.mu.Lock()
	c.localHTTPClient = client
	c.mu.Unlock()
}

func (c *Client) stableAuthWindow() time.Duration {
	if c.authRetryWindow > 0 {
		return c.authRetryWindow
	}
	return authRetryStableWindow
}

func (c *Client) shutdownGrace() time.Duration {
	if c.shutdownGracePeriod > 0 {
		return c.shutdownGracePeriod
	}
	return shutdownGraceTimeout
}

func (c *Client) streamCloseDeadline() time.Duration {
	if c.streamCloseTimeout > 0 {
		return c.streamCloseTimeout
	}
	return streamWaitDoneTimeout
}

// ConnectWithRetry establishes and maintains the tunnel until ctx is canceled,
// a fatal (non-retryable) error occurs, or the server requests clean closure.
func (c *Client) ConnectWithRetry(ctx context.Context) error {
	backoff := c.cfg.InitialBackoff
	authRetried := false

	for {
		connectedDuration, err := c.connectOnce(ctx)

		if err == nil || ctx.Err() != nil {
			return nil
		}

		if connectedDuration > c.cfg.MaxBackoff {
			backoff = c.cfg.InitialBackoff
		}
		if connectedDuration >= c.stableAuthWindow() {
			authRetried = false
		}

		wait, shouldRetry, fatalErr := c.classifyDisconnect(err, backoff, &authRetried)
		if !shouldRetry {
			if c.cfg.Events.OnError != nil {
				c.cfg.Events.OnError(fatalErr)
			}
			return fatalErr
		}
		backoff = wait

		c.logger.Error("tunnel disconnected, reconnecting", "err", err, "backoff", backoff, "was_connected", connectedDuration)
		if c.cfg.Events.OnReconnecting != nil {
			c.cfg.Events.OnReconnecting(err, backoff)
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
			backoff = min(backoff*2, c.cfg.MaxBackoff)
		}
	}
}

func (c *Client) classifyDisconnect(err error, currentBackoff time.Duration, authRetried *bool) (time.Duration, bool, error) {
	var closeErr *ServerCloseError
	if errors.As(err, &closeErr) {
		switch closeErr.Code {
		case pb.CloseCode_CLOSE_CODE_UNSPECIFIED:
			return currentBackoff, false, nil
		case pb.CloseCode_CLOSE_CODE_EVICTED:
			return 0, false, err
		case pb.CloseCode_CLOSE_CODE_AUTH_EXPIRED:
			if *authRetried {
				return 0, false, err
			}
			if inv, ok := c.cfg.TokenSource.(InvalidatableTokenSource); ok {
				inv.Invalidate()
				*authRetried = true
				return c.cfg.InitialBackoff, true, nil
			}
			return 0, false, err
		case pb.CloseCode_CLOSE_CODE_SERVER_SHUTDOWN, pb.CloseCode_CLOSE_CODE_HEARTBEAT_TIMEOUT:
			return currentBackoff, true, nil
		default:
			return currentBackoff, true, nil
		}
	}

	if errors.Is(err, ErrInsecureHTTP) {
		return 0, false, err
	}
	switch connect.CodeOf(err) {
	case connect.CodeAlreadyExists, connect.CodePermissionDenied, connect.CodeInvalidArgument:
		return 0, false, err
	case connect.CodeUnauthenticated:
		if *authRetried {
			return 0, false, err
		}
		if inv, ok := c.cfg.TokenSource.(InvalidatableTokenSource); ok {
			inv.Invalidate()
			*authRetried = true
			return c.cfg.InitialBackoff, true, nil
		}
		return 0, false, err
	default:
		return currentBackoff, true, nil
	}
}

// Connect opens a single Tunnel stream, performs registration, and serves requests
// using the negotiated protocol version (v3 reverse HTTP/2 or legacy v2).
func (c *Client) Connect(ctx context.Context) error {
	_, err := c.connectOnce(ctx)
	return err
}

func (c *Client) connectOnce(ctx context.Context) (time.Duration, error) {
	streamCtx, streamCancel := context.WithCancel(context.WithoutCancel(ctx))
	defer streamCancel()
	stopHandshakeCancel := context.AfterFunc(ctx, streamCancel)
	defer stopHandshakeCancel()

	rpcClient := divergev1alpha1connect.NewTunnelServiceClient(
		c.TunnelHTTPClient(),
		c.cfg.ServerAddr,
	)
	stream := rpcClient.Tunnel(streamCtx)

	if err := stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Register{
			Register: &pb.TunnelRegister{
				Service:          c.cfg.Service,
				Namespace:        c.cfg.Namespace,
				Port:             int32(c.cfg.LocalPort),
				PreviewId:        c.cfg.PreviewID,
				ProtocolVersion:  ProtocolVersion,
				Metadata:         c.cfg.Metadata,
				Force:            c.cfg.Force,
				SkipK8SService:   c.cfg.SkipK8sService,
				ClientInstanceId: c.cfg.ClientInstanceID,
			},
		},
	}); err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("failed to send register: %w", err)
	}

	msg, err := stream.Receive()
	if err != nil {
		return 0, fmt.Errorf("failed to receive ready: %w", err)
	}
	if closeMsg := msg.GetClose(); closeMsg != nil {
		if closeMsg.Code != pb.CloseCode_CLOSE_CODE_UNSPECIFIED {
			return 0, &ServerCloseError{Code: closeMsg.Code, Reason: closeMsg.Reason}
		}
		if closeMsg.Reason != "" {
			return 0, errors.New(closeMsg.Reason)
		}
		return 0, nil
	}

	ready := msg.GetReady()
	if ready == nil {
		return 0, fmt.Errorf("expected TunnelReady, got %T", msg.Payload)
	}
	readyAt := time.Now()

	// After TunnelReady arrives, response headers have completed and Connect's
	// response body is initialized, so CloseResponse is safe and non-blocking.
	var closeRespOnce sync.Once
	closeResp := func() {
		closeRespOnce.Do(func() {
			_ = stream.CloseResponse()
		})
	}
	var abortOnce sync.Once
	abortStream := func() {
		abortOnce.Do(func() {
			streamCancel()
			_ = stream.CloseRequest()
			closeResp()
		})
	}
	defer func() {
		streamCancel()
		closeResp()
	}()

	protoVer := ready.ProtocolVersion
	if protoVer == 0 {
		// Servers from v0.28.0 and earlier omit ProtocolVersion (0) for v2 sessions.
		protoVer = 2
	}

	info := ReadyInfo{
		TunnelID:        ready.TunnelId,
		Endpoint:        ready.Endpoint,
		ProxyService:    ready.ProxyService,
		K8sServiceName:  ready.K8SServiceName,
		ProtocolVersion: protoVer,
	}
	c.mu.Lock()
	c.readyInfo = info
	c.mu.Unlock()

	if !stopHandshakeCancel() {
		_ = bidiCloseOnEarlyCancel(stream)
		return 0, nil
	}

	c.logger.Info("tunnel ready",
		"tunnel_id", info.TunnelID,
		"endpoint", info.Endpoint,
		"proxy_service", info.ProxyService,
		"k8s_service_name", info.K8sServiceName,
		"protocol_version", info.ProtocolVersion,
	)
	c.readyOnce.Do(func() { close(c.Ready) })
	if c.cfg.Events.OnReady != nil {
		c.cfg.Events.OnReady(info)
	}

	var disconnectedAt time.Time
	var serveErr error
	if protoVer >= ProtocolVersion {
		disconnectedAt, serveErr = c.serveV3(ctx, stream, abortStream)
	} else {
		disconnectedAt, serveErr = c.serveV2(ctx, stream, abortStream)
	}
	if disconnectedAt.IsZero() {
		disconnectedAt = time.Now()
	}
	return max(0, disconnectedAt.Sub(readyAt)), serveErr
}

func bidiCloseOnEarlyCancel(stream *connect.BidiStreamForClient[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
	_ = stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Close{
			Close: &pb.TunnelClose{
				Code:   pb.CloseCode_CLOSE_CODE_UNSPECIFIED,
				Reason: "client shutting down",
			},
		},
	})
	return stream.CloseRequest()
}

type v3Stream struct {
	connectClientPacketStream
	mu      sync.Mutex
	readErr error
}

func (s *v3Stream) RecvPacket() ([]byte, *pb.TunnelClose, error) {
	raw, closeMsg, err := s.connectClientPacketStream.RecvPacket()
	if err != nil {
		s.mu.Lock()
		s.readErr = err
		s.mu.Unlock()
	}
	return raw, closeMsg, err
}

func (s *v3Stream) ReadErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readErr
}

func (c *Client) serveV3(
	ctx context.Context,
	stream *connect.BidiStreamForClient[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse],
	abortStream func(),
) (time.Time, error) {
	rp, err := newLocalReverseProxy(c.cfg.LocalHost, c.cfg.LocalPort, c.cfg.DialRetryTimeout, c.cfg.LocalTransport, c.logger)
	if err != nil {
		return time.Now(), err
	}

	trackedStream := &v3Stream{connectClientPacketStream: connectClientPacketStream{stream: stream}}
	bidiConn := newBidiStreamConn(trackedStream)
	httpSrv := newHTTP2TunnelServer(rp, c.logger)
	ln := newSingleConnListener(bidiConn)

	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- httpSrv.Serve(ln)
	}()

	grace := c.shutdownGrace()
	closeTimeout := c.streamCloseDeadline()

	select {
	case <-ctx.Done():
		disconnectedAt := time.Now()
		hardCancel := time.AfterFunc(grace+closeTimeout, abortStream)
		defer hardCancel.Stop()

		shutCtx, shutCancel := context.WithTimeout(context.Background(), grace)
		shutErr := httpSrv.Shutdown(shutCtx)
		shutCancel()
		hardCancel.Reset(closeTimeout)
		if shutErr != nil {
			_ = httpSrv.Close()
		}
		_ = bidiConn.SendClose(pb.CloseCode_CLOSE_CODE_UNSPECIFIED, "client shutting down")
		bidiConn.CloseStream()
		_ = stream.CloseRequest()
		<-serveErrCh
		bidiConn.WaitDone(200 * time.Millisecond)
		return disconnectedAt, nil

	case evt := <-bidiConn.RemoteCloseCh():
		disconnectedAt := time.Now()
		hardCancel := time.AfterFunc(grace+closeTimeout, abortStream)
		defer hardCancel.Stop()

		shutCtx, shutCancel := context.WithTimeout(context.Background(), grace)
		shutErr := httpSrv.Shutdown(shutCtx)
		shutCancel()
		hardCancel.Reset(closeTimeout)
		if shutErr != nil {
			_ = httpSrv.Close()
		}
		bidiConn.CloseStream()
		_ = stream.CloseRequest()
		<-serveErrCh
		c.notifyClosed(&evt)
		if evt.Code == pb.CloseCode_CLOSE_CODE_UNSPECIFIED {
			return disconnectedAt, nil
		}
		return disconnectedAt, &evt

	case serveErr := <-serveErrCh:
		disconnectedAt := time.Now()
		if ctx.Err() == nil {
			bidiConn.WaitDone(closeTimeout)
		}
		hardCancel := time.AfterFunc(closeTimeout, abortStream)
		defer hardCancel.Stop()

		if evt, ok := bidiConn.RemoteClose(); ok {
			bidiConn.CloseStream()
			_ = stream.CloseRequest()
			c.notifyClosed(evt)
			if evt.Code == pb.CloseCode_CLOSE_CODE_UNSPECIFIED {
				return disconnectedAt, nil
			}
			return disconnectedAt, evt
		}
		bidiConn.CloseStream()
		_ = stream.CloseRequest()
		if readErr := trackedStream.ReadErr(); readErr != nil {
			return disconnectedAt, readErr
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && !errors.Is(serveErr, net.ErrClosed) {
			return disconnectedAt, serveErr
		}
		return disconnectedAt, io.EOF
	}
}

func (c *Client) notifyClosed(evt *ServerCloseError) {
	if evt == nil {
		return
	}
	c.logger.Info("tunnel closed by server", "code", evt.Code.String(), "reason", evt.Reason)
	if c.cfg.Events.OnClosed != nil {
		c.cfg.Events.OnClosed(evt.Code, evt.Reason)
	}
}

type v2StreamResult struct {
	err error
	at  time.Time
}

func (c *Client) serveV2(
	ctx context.Context,
	stream *connect.BidiStreamForClient[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse],
	abortStream func(),
) (time.Time, error) {
	var sendMu sync.Mutex
	errCh := make(chan v2StreamResult, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.logger.Error("panic in tunnel v2 receive loop", "err", r)
				errCh <- v2StreamResult{err: fmt.Errorf("panic: %v", r), at: time.Now()}
			}
		}()

		for {
			msg, err := stream.Receive()
			if err != nil {
				at := time.Now()
				c.logger.Error("stream receive error", "err", err)
				errCh <- v2StreamResult{err: err, at: at}
				return
			}

			switch p := msg.Payload.(type) {
			case *pb.TunnelServiceTunnelResponse_Ping:
				sendMu.Lock()
				_ = stream.Send(&pb.TunnelServiceTunnelRequest{
					Payload: &pb.TunnelServiceTunnelRequest_Pong{
						Pong: &pb.TunnelPong{
							Timestamp: p.Ping.Timestamp,
						},
					},
				})
				sendMu.Unlock()
			case *pb.TunnelServiceTunnelResponse_HttpRequest:
				go func(req *pb.TunnelHTTPRequest) {
					defer func() {
						if r := recover(); r != nil {
							c.logger.Error("panic handling v2 tunnel request", "err", r)
						}
					}()
					c.handleV2HTTPRequest(ctx, req, &sendMu, stream)
				}(p.HttpRequest)
			case *pb.TunnelServiceTunnelResponse_Close:
				at := time.Now()
				evt := &ServerCloseError{Code: p.Close.Code, Reason: p.Close.Reason}
				c.notifyClosed(evt)
				if evt.Code == pb.CloseCode_CLOSE_CODE_UNSPECIFIED {
					errCh <- v2StreamResult{err: nil, at: at}
				} else {
					errCh <- v2StreamResult{err: evt, at: at}
				}
				return
			}
		}
	}()

	var disconnectedAt time.Time
	var streamErr error
	select {
	case <-ctx.Done():
		disconnectedAt = time.Now()
	case res := <-errCh:
		streamErr = res.err
		disconnectedAt = res.at
	}

	hardCancel := time.AfterFunc(c.streamCloseDeadline(), abortStream)
	defer hardCancel.Stop()

	sendMu.Lock()
	defer sendMu.Unlock()

	_ = stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Close{
			Close: &pb.TunnelClose{
				Reason: "client shutting down",
			},
		},
	})

	err := stream.CloseRequest()
	if streamErr != nil {
		return disconnectedAt, streamErr
	}
	if ctx.Err() != nil {
		return disconnectedAt, nil
	}
	return disconnectedAt, err
}

func (c *Client) localClient() *http.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.localHTTPClient
}

func (c *Client) handleV2HTTPRequest(
	ctx context.Context,
	req *pb.TunnelHTTPRequest,
	sendMu *sync.Mutex,
	stream *connect.BidiStreamForClient[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse],
) {
	rawPath := req.Path
	if rawPath == "" {
		rawPath = "/"
	}
	parsed, err := url.ParseRequestURI(rawPath)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || !strings.HasPrefix(parsed.Path, "/") {
		c.sendV2ErrorResponse(sendMu, stream, req.RequestId, http.StatusBadRequest, "invalid request path")
		return
	}

	targetHost := net.JoinHostPort(strings.Trim(c.cfg.LocalHost, "[]"), strconv.Itoa(c.cfg.LocalPort))
	targetURL := &url.URL{
		Scheme:   "http",
		Host:     targetHost,
		Path:     parsed.Path,
		RawQuery: parsed.RawQuery,
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL.String(), bytes.NewReader(req.Body))
	if err != nil {
		c.sendV2ErrorResponse(sendMu, stream, req.RequestId, http.StatusInternalServerError, fmt.Sprintf("failed to create request: %v", err))
		return
	}

	for _, h := range req.Headers {
		for _, v := range h.Values {
			if strings.HasPrefix(strings.ToLower(h.Key), "x-diverge-") {
				continue
			}
			httpReq.Header.Add(h.Key, v)
		}
	}

	resp, err := c.localClient().Do(httpReq)
	if err != nil {
		c.sendV2ErrorResponse(sendMu, stream, req.RequestId, http.StatusBadGateway, "local service unavailable")
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	headers := headersToProto(resp.Header)

	buf := make([]byte, v2ChunkSize)
	n, readErr := io.ReadFull(resp.Body, buf)
	firstChunk := buf[:n]

	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		c.sendV2ErrorResponse(sendMu, stream, req.RequestId, http.StatusBadGateway, "error reading local response body")
		return
	}

	if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) || n < v2ChunkSize {
		sendMu.Lock()
		_ = stream.Send(&pb.TunnelServiceTunnelRequest{
			Payload: &pb.TunnelServiceTunnelRequest_HttpResponse{
				HttpResponse: &pb.TunnelHTTPResponse{
					RequestId:     req.RequestId,
					StatusCode:    int32(resp.StatusCode),
					Headers:       headers,
					Body:          firstChunk,
					HasMoreChunks: false,
				},
			},
		})
		sendMu.Unlock()
		return
	}

	sendMu.Lock()
	_ = stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_HttpResponse{
			HttpResponse: &pb.TunnelHTTPResponse{
				RequestId:     req.RequestId,
				StatusCode:    int32(resp.StatusCode),
				Headers:       headers,
				Body:          firstChunk,
				HasMoreChunks: true,
			},
		},
	})
	sendMu.Unlock()

	for {
		n, readErr = resp.Body.Read(buf)
		if n > 0 {
			isLast := errors.Is(readErr, io.EOF)
			sendMu.Lock()
			_ = stream.Send(&pb.TunnelServiceTunnelRequest{
				Payload: &pb.TunnelServiceTunnelRequest_ResponseChunk{
					ResponseChunk: &pb.TunnelResponseChunk{
						RequestId: req.RequestId,
						Data:      buf[:n],
						IsLast:    isLast,
					},
				},
			})
			sendMu.Unlock()
			if isLast {
				return
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				c.logger.Error("error reading remaining local response body", "err", readErr)
			}
			sendMu.Lock()
			_ = stream.Send(&pb.TunnelServiceTunnelRequest{
				Payload: &pb.TunnelServiceTunnelRequest_ResponseChunk{
					ResponseChunk: &pb.TunnelResponseChunk{
						RequestId: req.RequestId,
						Data:      nil,
						IsLast:    true,
					},
				},
			})
			sendMu.Unlock()
			return
		}
	}
}

func (c *Client) sendV2ErrorResponse(
	sendMu *sync.Mutex,
	stream *connect.BidiStreamForClient[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse],
	requestID string,
	statusCode int32,
	msg string,
) {
	sendMu.Lock()
	_ = stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_HttpResponse{
			HttpResponse: &pb.TunnelHTTPResponse{
				RequestId:  requestID,
				StatusCode: statusCode,
				Body:       []byte(msg),
			},
		},
	})
	sendMu.Unlock()
}

func headersToProto(h http.Header) []*pb.HeaderEntry {
	entries := make([]*pb.HeaderEntry, 0, len(h))
	for k, v := range h {
		entries = append(entries, &pb.HeaderEntry{
			Key:    k,
			Values: v,
		})
	}
	return entries
}

// ListTunnels queries TunnelService.ListTunnels using httpClient.
func ListTunnels(ctx context.Context, httpClient *http.Client, serverAddr string, req *pb.ListTunnelsRequest) (*pb.ListTunnelsResponse, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if req == nil {
		req = &pb.ListTunnelsRequest{}
	}
	client := divergev1alpha1connect.NewTunnelServiceClient(httpClient, serverAddr)
	resp, err := client.ListTunnels(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
