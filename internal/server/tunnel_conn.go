package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
)

const (
	// maxTunnelRawFrameSize bounds each Protocol v3 raw_data protobuf message payload.
	maxTunnelRawFrameSize = 64 << 10 // 64 KiB
	// defaultCloseFrameTimeout bounds waiting for a best-effort TunnelClose send during teardown.
	defaultCloseFrameTimeout = 250 * time.Millisecond
)

var (
	errResponseHeaderTimeout = errors.New("tunnel upstream response header timeout")
	errStreamIdleTimeout     = errors.New("tunnel upstream stream idle timeout")
)

// TunnelCloseEvent records a structured TunnelClose frame received from the peer.
type TunnelCloseEvent struct {
	Code   pb.CloseCode
	Reason string
}

type tunnelPacketStream interface {
	RecvPacket() (rawData []byte, closeMsg *pb.TunnelClose, err error)
	SendRawData(data []byte) error
	SendClose(closeMsg *pb.TunnelClose) error
}

type serverPacketStream struct {
	stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]
}

func (s *serverPacketStream) RecvPacket() ([]byte, *pb.TunnelClose, error) {
	for {
		msg, err := s.stream.Receive()
		if err != nil {
			return nil, nil, err
		}
		switch p := msg.Payload.(type) {
		case *pb.TunnelServiceTunnelRequest_RawData:
			if len(p.RawData) > 0 {
				return p.RawData, nil, nil
			}
		case *pb.TunnelServiceTunnelRequest_Close:
			if p.Close == nil {
				return nil, &pb.TunnelClose{}, nil
			}
			return nil, p.Close, nil
		}
	}
}

func (s *serverPacketStream) SendRawData(data []byte) error {
	return s.stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_RawData{
			RawData: data,
		},
	})
}

func (s *serverPacketStream) SendClose(closeMsg *pb.TunnelClose) error {
	return s.stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_Close{
			Close: closeMsg,
		},
	})
}

type tunnelPipeAddr string

func (tunnelPipeAddr) Network() string  { return "connect-bidi" }
func (a tunnelPipeAddr) String() string { return string(a) }

type closeFrameReq struct {
	msg  *pb.TunnelClose
	done chan error
}

// bidiStreamConn adapts a ConnectRPC bidi stream carrying raw_data + TunnelClose
// frames into a net.Conn suitable for stdlib net/http unencrypted HTTP/2 (h2c).
//
// Reads and writes are decoupled through bounded buffers so Close and
// CloseStream are lock-free and never deadlock against a stalled outer HTTP/2
// stream send, preventing mutual H2-over-H2 flow-control hangs.
type bidiStreamConn struct {
	stream tunnelPacketStream
	pr     *io.PipeReader
	pw     *io.PipeWriter

	writeMu      sync.Mutex
	sendCh       chan []byte
	closeFrameCh chan closeFrameReq
	inCh         chan []byte

	closeCh       chan struct{}
	streamCloseCh chan struct{}
	writeErrCh    chan struct{}
	writeErr      atomic.Pointer[error]

	connClosed   atomic.Bool
	streamClosed atomic.Bool
	closeSent    atomic.Bool
	draining     atomic.Bool

	closeOnce       sync.Once
	streamCloseOnce sync.Once
	writeErrOnce    sync.Once

	doneCh      chan struct{}
	writeDoneCh chan struct{}

	onCloseMu sync.Mutex
	onClose   func()

	remoteMu       sync.Mutex
	remoteClose    *TunnelCloseEvent
	remoteCloseEvt chan TunnelCloseEvent
}

func newBidiStreamConn(stream tunnelPacketStream) *bidiStreamConn {
	pr, pw := io.Pipe()
	c := &bidiStreamConn{
		stream:         stream,
		pr:             pr,
		pw:             pw,
		sendCh:         make(chan []byte, 64),
		closeFrameCh:   make(chan closeFrameReq, 1),
		inCh:           make(chan []byte, 64),
		closeCh:        make(chan struct{}),
		streamCloseCh:  make(chan struct{}),
		writeErrCh:     make(chan struct{}),
		doneCh:         make(chan struct{}),
		writeDoneCh:    make(chan struct{}),
		remoteCloseEvt: make(chan TunnelCloseEvent, 1),
	}
	go c.readLoop()
	go c.pipeFeedLoop()
	go c.writeLoop()
	return c
}

// SetOnClose registers a callback invoked once when the underlying net.Conn is closed.
func (c *bidiStreamConn) SetOnClose(fn func()) {
	c.onCloseMu.Lock()
	defer c.onCloseMu.Unlock()
	if c.connClosed.Load() {
		if fn != nil {
			go fn()
		}
		return
	}
	c.onClose = fn
}

// BeginDrain instructs readLoop to keep reading control frames (like TunnelClose)
// even after HTTP/2 closes the internal io.Pipe during graceful Shutdown/GOAWAY.
func (c *bidiStreamConn) BeginDrain() {
	c.draining.Store(true)
}

func (c *bidiStreamConn) failWrites(err error) {
	if err == nil {
		err = net.ErrClosed
	}
	c.writeErrOnce.Do(func() {
		e := err
		c.writeErr.Store(&e)
		close(c.writeErrCh)
	})
}

func (c *bidiStreamConn) loadWriteErr() error {
	if p := c.writeErr.Load(); p != nil && *p != nil {
		return *p
	}
	return net.ErrClosed
}

func (c *bidiStreamConn) readLoop() {
	defer close(c.doneCh)
	defer close(c.inCh)

	pipeOpen := true
	for {
		if c.streamClosed.Load() {
			return
		}
		raw, closeMsg, err := c.stream.RecvPacket()
		if closeMsg != nil {
			evt := TunnelCloseEvent{
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
		if c.connClosed.Load() {
			if c.draining.Load() && !c.streamClosed.Load() {
				pipeOpen = false
				continue
			}
			return
		}

		select {
		case c.inCh <- raw:
		case <-c.closeCh:
			if c.draining.Load() && !c.streamClosed.Load() {
				pipeOpen = false
				continue
			}
			return
		case <-c.streamCloseCh:
			return
		}
	}
}

func (c *bidiStreamConn) pipeFeedLoop() {
	for chunk := range c.inCh {
		if c.connClosed.Load() {
			return
		}
		if _, err := c.pw.Write(chunk); err != nil {
			return
		}
	}
}

func (c *bidiStreamConn) writeLoop() {
	defer close(c.writeDoneCh)
	for {
		select {
		case <-c.streamCloseCh:
			c.failWrites(net.ErrClosed)
			return
		default:
		}

		if c.connClosed.Load() {
			// Once the net.Conn closes, only TunnelClose frames remain eligible.
			select {
			case <-c.streamCloseCh:
				c.failWrites(net.ErrClosed)
				return
			case req := <-c.closeFrameCh:
				err := c.stream.SendClose(req.msg)
				if err != nil {
					c.failWrites(err)
				}
				if req.done != nil {
					req.done <- err
				}
				if err != nil {
					return
				}
			}
			continue
		}

		select {
		case <-c.streamCloseCh:
			c.failWrites(net.ErrClosed)
			return
		case req := <-c.closeFrameCh:
			err := c.stream.SendClose(req.msg)
			if err != nil {
				c.failWrites(err)
			}
			if req.done != nil {
				req.done <- err
			}
			if err != nil {
				return
			}
		case data := <-c.sendCh:
			if err := c.stream.SendRawData(data); err != nil {
				c.failWrites(err)
				return
			}
		case <-c.closeCh:
		}
	}
}

// Read reads raw HTTP/2 frame bytes received over the ConnectRPC stream.
func (c *bidiStreamConn) Read(p []byte) (int, error) {
	return c.pr.Read(p)
}

// Write serializes HTTP/2 frame bytes into raw_data ConnectRPC frames.
func (c *bidiStreamConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	written := 0
	for len(p) > 0 {
		if c.connClosed.Load() || c.streamClosed.Load() {
			return written, net.ErrClosed
		}
		n := len(p)
		if n > maxTunnelRawFrameSize {
			n = maxTunnelRawFrameSize
		}
		chunk := make([]byte, n)
		copy(chunk, p[:n])

		select {
		case c.sendCh <- chunk:
		case <-c.closeCh:
			return written, net.ErrClosed
		case <-c.streamCloseCh:
			return written, net.ErrClosed
		case <-c.writeErrCh:
			return written, c.loadWriteErr()
		}

		written += n
		p = p[n:]
	}
	return written, nil
}

// SendClose transmits a structured TunnelClose frame before stream teardown with a bounded timeout.
func (c *bidiStreamConn) SendClose(code pb.CloseCode, reason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultCloseFrameTimeout)
	defer cancel()
	return c.SendCloseContext(ctx, code, reason)
}

// SendCloseContext transmits a structured TunnelClose frame bounded by ctx.
func (c *bidiStreamConn) SendCloseContext(ctx context.Context, code pb.CloseCode, reason string) error {
	if c.streamClosed.Load() || !c.closeSent.CompareAndSwap(false, true) {
		return nil
	}
	req := closeFrameReq{
		msg: &pb.TunnelClose{
			Code:   code,
			Reason: reason,
		},
		done: make(chan error, 1),
	}

	select {
	case c.closeFrameCh <- req:
	case <-c.streamCloseCh:
		return nil
	case <-c.writeDoneCh:
		return c.loadWriteErr()
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-req.done:
		return err
	case <-c.streamCloseCh:
		return nil
	case <-c.writeDoneCh:
		return c.loadWriteErr()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close closes the HTTP/2 net.Conn pipe without preventing a subsequent SendClose on the Connect stream.
func (c *bidiStreamConn) Close() error {
	c.closeOnce.Do(func() {
		c.connClosed.Store(true)
		close(c.closeCh)
		_ = c.pw.CloseWithError(net.ErrClosed)
		_ = c.pr.CloseWithError(net.ErrClosed)

		c.onCloseMu.Lock()
		fn := c.onClose
		c.onClose = nil
		c.onCloseMu.Unlock()
		if fn != nil {
			fn()
		}
	})
	return nil
}

// CloseStream marks the underlying ConnectRPC stream closed before the handler returns.
func (c *bidiStreamConn) CloseStream() {
	c.streamCloseOnce.Do(func() {
		c.streamClosed.Store(true)
		close(c.streamCloseCh)
	})
	_ = c.Close()
}

// Done returns a channel closed once the inbound receive loop terminates.
func (c *bidiStreamConn) Done() <-chan struct{} {
	return c.doneCh
}

// WaitDone waits up to timeout for the inbound receive loop to terminate.
func (c *bidiStreamConn) WaitDone(timeout time.Duration) bool {
	return waitSignal(c.doneCh, timeout)
}

// WaitWriteDone waits up to timeout for the outbound writeLoop to exit so no
// stream.Send calls outlive the owning ConnectRPC handler.
func (c *bidiStreamConn) WaitWriteDone(timeout time.Duration) bool {
	return waitSignal(c.writeDoneCh, timeout)
}

func waitSignal(ch <-chan struct{}, timeout time.Duration) bool {
	if timeout <= 0 {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	}
}

// RemoteClose returns the out-of-band TunnelClose event if the peer sent one.
func (c *bidiStreamConn) RemoteClose() (TunnelCloseEvent, bool) {
	c.remoteMu.Lock()
	defer c.remoteMu.Unlock()
	if c.remoteClose == nil {
		return TunnelCloseEvent{}, false
	}
	return *c.remoteClose, true
}

func (c *bidiStreamConn) LocalAddr() net.Addr                { return tunnelPipeAddr("tunnel-local") }
func (c *bidiStreamConn) RemoteAddr() net.Addr               { return tunnelPipeAddr("tunnel-remote") }
func (c *bidiStreamConn) SetDeadline(_ time.Time) error      { return nil }
func (c *bidiStreamConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *bidiStreamConn) SetWriteDeadline(_ time.Time) error { return nil }

// newTunnelHTTP2ClientConn wraps an established stream net.Conn in a Go stdlib
// unencrypted HTTP/2 (h2c) ClientConn with optional idle PING health checks.
func newTunnelHTTP2ClientConn(ctx context.Context, conn net.Conn, sendPingTimeout, pingTimeout time.Duration) (*http.ClientConn, error) {
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)

	var dialOnce sync.Once
	tr := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			var c net.Conn
			dialOnce.Do(func() {
				c = conn
			})
			if c == nil {
				return nil, errors.New("tunnel connection already dialed")
			}
			return c, nil
		},
		Protocols: &protocols,
		HTTP2: &http.HTTP2Config{
			SendPingTimeout:  sendPingTimeout,
			PingTimeout:      pingTimeout,
			WriteByteTimeout: 30 * time.Second,
		},
	}
	return tr.NewClientConn(ctx, "http", "tunnel.local:80")
}

// headerTimeoutRoundTripper bounds waiting for upstream response headers after
// request body transmission completes, without limiting long-lived response streams.
type headerTimeoutRoundTripper struct {
	base        http.RoundTripper
	timeout     time.Duration
	idleTimeout time.Duration
}

type bodyEOFReadCloser struct {
	io.ReadCloser
	onDone func()
	once   sync.Once
}

func (b *bodyEOFReadCloser) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(b.onDone)
	}
	return n, err
}

func (b *bodyEOFReadCloser) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.onDone)
	return err
}

func (rt *headerTimeoutRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.timeout <= 0 && rt.idleTimeout <= 0 {
		return rt.base.RoundTrip(req)
	}

	ctx, cancel := context.WithCancelCause(req.Context())
	var state atomic.Int32 // 0 = pending, 1 = completed, 2 = timedOut
	var timerMu sync.Mutex
	var timer *time.Timer
	var armed bool

	armTimer := func() {
		if rt.timeout <= 0 {
			return
		}
		timerMu.Lock()
		defer timerMu.Unlock()
		if armed || state.Load() != 0 {
			return
		}
		armed = true
		timer = time.AfterFunc(rt.timeout, func() {
			if state.CompareAndSwap(0, 2) {
				cancel(errResponseHeaderTimeout)
			}
		})
	}
	stopTimer := func() {
		timerMu.Lock()
		t := timer
		armed = true
		timerMu.Unlock()
		if t != nil {
			t.Stop()
		}
	}

	outReq := req.Clone(ctx)
	if outReq.Body != nil && outReq.Body != http.NoBody && rt.timeout > 0 {
		outReq.Body = &bodyEOFReadCloser{
			ReadCloser: outReq.Body,
			onDone:     armTimer,
		}
	} else {
		armTimer()
	}

	resp, err := rt.base.RoundTrip(outReq)
	stopTimer()

	if !state.CompareAndSwap(0, 1) {
		cancel(errResponseHeaderTimeout)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, errResponseHeaderTimeout
	}
	if err != nil {
		cancel(err)
		return nil, err
	}

	if resp != nil && resp.Body != nil {
		body := resp.Body
		if rt.idleTimeout > 0 {
			body = newIdleTimeoutReadCloser(body, rt.idleTimeout, func() {
				cancel(errStreamIdleTimeout)
			})
		}
		resp.Body = &cancelOnCloseReadCloser{
			ReadCloser: body,
			onClose:    func() { cancel(nil) },
		}
	} else {
		cancel(nil)
	}
	return resp, nil
}

type cancelOnCloseReadCloser struct {
	io.ReadCloser
	once    sync.Once
	onClose func()
}

func (c *cancelOnCloseReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.once.Do(func() {
		if c.onClose != nil {
			c.onClose()
		}
	})
	return err
}

// idleTimeoutReadCloser aborts a streaming response body only when no data arrives
// for more than timeout between Read calls, leaving active streams unlimited.
type idleTimeoutReadCloser struct {
	io.ReadCloser
	timeout   time.Duration
	onTimeout func()
	closeOnce sync.Once
	closeErr  error
}

func newIdleTimeoutReadCloser(rc io.ReadCloser, timeout time.Duration, onTimeout func()) *idleTimeoutReadCloser {
	return &idleTimeoutReadCloser{
		ReadCloser: rc,
		timeout:    timeout,
		onTimeout:  onTimeout,
	}
}

func (rc *idleTimeoutReadCloser) Read(p []byte) (int, error) {
	if rc.timeout <= 0 {
		return rc.ReadCloser.Read(p)
	}

	var readState atomic.Int32 // 0 = pending, 1 = returned, 2 = timedOut
	timer := time.AfterFunc(rc.timeout, func() {
		if readState.CompareAndSwap(0, 2) {
			if rc.onTimeout != nil {
				rc.onTimeout()
			}
			_ = rc.Close()
		}
	})

	n, err := rc.ReadCloser.Read(p)
	won := readState.CompareAndSwap(0, 1)
	timer.Stop()
	if !won && err != nil && !errors.Is(err, io.EOF) {
		return n, errStreamIdleTimeout
	}
	return n, err
}

func (rc *idleTimeoutReadCloser) Close() error {
	rc.closeOnce.Do(func() {
		rc.closeErr = rc.ReadCloser.Close()
	})
	return rc.closeErr
}
