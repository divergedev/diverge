package tunnelclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
)

const (
	maxTunnelRawFrameSize    = 64 << 10 // 64 KiB
	defaultDialRetryTimeout  = 2 * time.Second
	dialRetryBackoff         = 100 * time.Millisecond
	localRespHeaderTimeout   = 30 * time.Second
	http2ServerIdleTimeout   = 5 * time.Minute
	http2MaxConcurrentStream = 128
)

// ServerCloseError represents an out-of-band TunnelClose control frame received from the server.
type ServerCloseError struct {
	Code   pb.CloseCode
	Reason string
}

// Error implements the error interface.
func (e *ServerCloseError) Error() string {
	if e == nil {
		return ""
	}
	if e.Reason != "" {
		return fmt.Sprintf("tunnel closed by server (%s): %s", e.Code.String(), e.Reason)
	}
	return fmt.Sprintf("tunnel closed by server (%s)", e.Code.String())
}

type clientPacketStream interface {
	RecvPacket() (rawData []byte, closeMsg *pb.TunnelClose, err error)
	SendRawData(data []byte) error
	SendClose(closeMsg *pb.TunnelClose) error
}

type connectClientPacketStream struct {
	stream *connect.BidiStreamForClient[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]
}

func (s *connectClientPacketStream) RecvPacket() ([]byte, *pb.TunnelClose, error) {
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

func (s *connectClientPacketStream) SendRawData(data []byte) error {
	return s.stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_RawData{
			RawData: data,
		},
	})
}

func (s *connectClientPacketStream) SendClose(closeMsg *pb.TunnelClose) error {
	return s.stream.Send(&pb.TunnelServiceTunnelRequest{
		Payload: &pb.TunnelServiceTunnelRequest_Close{
			Close: closeMsg,
		},
	})
}

type pipeAddr string

func (pipeAddr) Network() string  { return "connect-bidi" }
func (a pipeAddr) String() string { return string(a) }

// bidiStreamConn adapts a ConnectRPC bidirectional stream carrying raw_data +
// TunnelClose frames into a net.Conn for Go's stdlib unencrypted HTTP/2 (h2c) server.
type bidiStreamConn struct {
	stream clientPacketStream
	pr     *io.PipeReader
	pw     *io.PipeWriter

	sendMu       sync.Mutex
	closed       bool
	streamClosed bool
	closeSent    bool

	closeOnce sync.Once
	doneCh    chan struct{}

	remoteMu       sync.Mutex
	remoteClose    *ServerCloseError
	remoteCloseEvt chan ServerCloseError
}

func newBidiStreamConn(stream clientPacketStream) *bidiStreamConn {
	pr, pw := io.Pipe()
	c := &bidiStreamConn{
		stream:         stream,
		pr:             pr,
		pw:             pw,
		doneCh:         make(chan struct{}),
		remoteCloseEvt: make(chan ServerCloseError, 1),
	}
	go c.readLoop()
	return c
}

func (c *bidiStreamConn) readLoop() {
	defer close(c.doneCh)
	pipeOpen := true
	for {
		raw, closeMsg, err := c.stream.RecvPacket()
		if closeMsg != nil {
			evt := ServerCloseError{
				Code:   closeMsg.Code,
				Reason: closeMsg.Reason,
			}
			c.remoteMu.Lock()
			c.remoteClose = &evt
			c.remoteMu.Unlock()
			select {
			case c.remoteCloseEvt <- evt:
			default:
			}
			_ = c.pw.CloseWithError(io.EOF)
			return
		}
		if err != nil {
			_ = c.pw.CloseWithError(err)
			return
		}
		if len(raw) == 0 || !pipeOpen {
			continue
		}
		if _, wErr := c.pw.Write(raw); wErr != nil {
			// HTTP/2 GOAWAY or local Close may shut the pipe before the peer's
			// out-of-band TunnelClose arrives on the Connect stream; keep draining
			// inbound frames until TunnelClose or stream EOF/cancel.
			pipeOpen = false
			continue
		}
	}
}

// Read reads raw HTTP/2 frame bytes received over the ConnectRPC stream.
func (c *bidiStreamConn) Read(p []byte) (int, error) {
	return c.pr.Read(p)
}

// Write serializes raw HTTP/2 frame bytes into bounded 64 KiB raw_data ConnectRPC frames.
func (c *bidiStreamConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	written := 0
	for len(p) > 0 {
		if c.closed {
			return written, net.ErrClosed
		}
		n := min(len(p), maxTunnelRawFrameSize)
		chunk := make([]byte, n)
		copy(chunk, p[:n])
		if err := c.stream.SendRawData(chunk); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// SendClose transmits an out-of-band TunnelClose frame before closing the Connect stream.
func (c *bidiStreamConn) SendClose(code pb.CloseCode, reason string) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.streamClosed || c.closeSent {
		return nil
	}
	c.closeSent = true
	return c.stream.SendClose(&pb.TunnelClose{
		Code:   code,
		Reason: reason,
	})
}

// Close closes the HTTP/2 net.Conn pipe without preventing a subsequent SendClose.
func (c *bidiStreamConn) Close() error {
	c.closeOnce.Do(func() {
		c.sendMu.Lock()
		c.closed = true
		c.sendMu.Unlock()

		_ = c.pw.CloseWithError(net.ErrClosed)
		_ = c.pr.CloseWithError(net.ErrClosed)
	})
	return nil
}

// CloseStream marks the underlying ConnectRPC stream closed during teardown.
func (c *bidiStreamConn) CloseStream() {
	c.sendMu.Lock()
	c.streamClosed = true
	c.sendMu.Unlock()
	_ = c.Close()
}

// WaitDone waits up to timeout for the inbound receive loop to exit.
func (c *bidiStreamConn) WaitDone(timeout time.Duration) bool {
	if timeout <= 0 {
		select {
		case <-c.doneCh:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-c.doneCh:
		return true
	case <-timer.C:
		return false
	}
}

// RemoteClose returns the out-of-band ServerCloseError if the server sent one.
func (c *bidiStreamConn) RemoteClose() (*ServerCloseError, bool) {
	c.remoteMu.Lock()
	defer c.remoteMu.Unlock()
	if c.remoteClose == nil {
		return nil, false
	}
	cp := *c.remoteClose
	return &cp, true
}

// RemoteCloseCh signals when an out-of-band TunnelClose control frame arrives.
func (c *bidiStreamConn) RemoteCloseCh() <-chan ServerCloseError {
	return c.remoteCloseEvt
}

func (c *bidiStreamConn) LocalAddr() net.Addr                { return pipeAddr("tunnel-client") }
func (c *bidiStreamConn) RemoteAddr() net.Addr               { return pipeAddr("tunnel-server") }
func (c *bidiStreamConn) SetDeadline(_ time.Time) error      { return nil }
func (c *bidiStreamConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *bidiStreamConn) SetWriteDeadline(_ time.Time) error { return nil }

type closeHookConn struct {
	net.Conn
	onClose func()
	once    sync.Once
}

func (c *closeHookConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.onClose)
	return err
}

// singleConnListener yields one net.Conn to http.Server.Serve and blocks on subsequent
// Accept calls until closed (either explicitly or when the yielded connection closes).
type singleConnListener struct {
	conn      net.Conn
	addr      net.Addr
	acceptMu  sync.Mutex
	accepted  bool
	closed    chan struct{}
	closeOnce sync.Once
}

func newSingleConnListener(conn net.Conn) *singleConnListener {
	ln := &singleConnListener{
		addr:   conn.LocalAddr(),
		closed: make(chan struct{}),
	}
	ln.conn = &closeHookConn{
		Conn:    conn,
		onClose: func() { _ = ln.Close() },
	}
	return ln
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	l.acceptMu.Lock()
	if !l.accepted {
		l.accepted = true
		c := l.conn
		l.acceptMu.Unlock()
		return c, nil
	}
	l.acceptMu.Unlock()
	<-l.closed
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closed)
	})
	return nil
}

func (l *singleConnListener) Addr() net.Addr { return l.addr }

func newHTTP2TunnelServer(handler http.Handler, logger *slog.Logger) *http.Server {
	if logger == nil {
		logger = slog.Default()
	}
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	return &http.Server{
		Handler:     handler,
		ErrorLog:    slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		Protocols:   &protocols,
		IdleTimeout: http2ServerIdleTimeout,
		HTTP2: &http.HTTP2Config{
			MaxConcurrentStreams: http2MaxConcurrentStream,
		},
	}
}

func isConnRefused(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "connection refused")
}

func newRetryingLoopbackTransport(retryTimeout time.Duration) *http.Transport {
	if retryTimeout < 0 {
		retryTimeout = 0
	}
	baseDialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return &http.Transport{
		DisableCompression:    true,
		ResponseHeaderTimeout: localRespHeaderTimeout,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			deadline := time.Now().Add(retryTimeout)
			for {
				conn, err := baseDialer.DialContext(ctx, network, addr)
				if err == nil {
					if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok && !tcpAddr.IP.IsLoopback() {
						_ = conn.Close()
						return nil, fmt.Errorf("refusing connection to non-loopback resolved IP %s", tcpAddr.IP)
					}
					return conn, nil
				}
				if ctx.Err() != nil || retryTimeout == 0 || !isConnRefused(err) || time.Now().After(deadline) {
					return nil, err
				}
				wait := min(dialRetryBackoff, time.Until(deadline))
				if wait <= 0 {
					return nil, err
				}
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				case <-timer.C:
				}
			}
		},
	}
}

// newLocalReverseProxy forwards requests received over the HTTP/2 tunnel stream
// strictly to a local loopback host:port without following HTTP redirects.
func newLocalReverseProxy(
	localHost string,
	localPort int,
	dialRetryTimeout time.Duration,
	baseTransport http.RoundTripper,
	logger *slog.Logger,
) (*httputil.ReverseProxy, error) {
	localHost = strings.TrimSpace(localHost)
	if localHost == "" {
		localHost = "127.0.0.1"
	}
	if !IsLoopbackAddress(localHost) {
		return nil, fmt.Errorf("local host %q must be a loopback address", localHost)
	}
	if localPort < 1 || localPort > 65535 {
		return nil, fmt.Errorf("local port %d out of valid range 1-65535", localPort)
	}
	if logger == nil {
		logger = slog.Default()
	}

	targetHost := net.JoinHostPort(strings.Trim(localHost, "[]"), strconv.Itoa(localPort))
	if baseTransport == nil {
		baseTransport = newRetryingLoopbackTransport(dialRetryTimeout)
	}

	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			for k := range pr.Out.Header {
				if strings.HasPrefix(strings.ToLower(k), "x-diverge-") {
					pr.Out.Header.Del(k)
				}
			}
			if xfh := pr.In.Header.Get("X-Forwarded-Host"); xfh != "" {
				pr.Out.Header.Set("X-Forwarded-Host", xfh)
			} else if pr.In.Host != "" {
				pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
			}
			if xfp := pr.In.Header.Get("X-Forwarded-Proto"); xfp != "" {
				pr.Out.Header.Set("X-Forwarded-Proto", xfp)
			}
			if xff := pr.In.Header.Values("X-Forwarded-For"); len(xff) > 0 {
				pr.Out.Header["X-Forwarded-For"] = append([]string(nil), xff...)
			}
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = targetHost
			pr.Out.Host = targetHost
		},
		Transport:     baseTransport,
		FlushInterval: -1,
		ErrorLog:      slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() == nil {
				logger.Debug("local service request failed", "target", targetHost, "path", r.URL.Path, "error", err)
			}
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "local service unavailable")
		},
	}, nil
}
