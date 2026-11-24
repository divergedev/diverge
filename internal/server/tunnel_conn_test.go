package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
)

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

func serveTunnelHTTP2(conn net.Conn, handler http.Handler) {
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{
		Handler:     handler,
		Protocols:   &protocols,
		IdleTimeout: 5 * time.Minute,
		HTTP2: &http.HTTP2Config{
			MaxConcurrentStreams: 128,
		},
	}
	_ = srv.Serve(newSingleConnListener(conn))
}

type pipePacketEndpoint struct {
	inCh      chan packetOrClose
	outCh     chan packetOrClose
	closeCh   chan struct{}
	closeOnce sync.Once
}

type packetOrClose struct {
	data  []byte
	close *pb.TunnelClose
}

func newPacketPipePair() (*pipePacketEndpoint, *pipePacketEndpoint) {
	aToB := make(chan packetOrClose, 64)
	bToA := make(chan packetOrClose, 64)
	doneA := make(chan struct{})
	doneB := make(chan struct{})
	a := &pipePacketEndpoint{inCh: bToA, outCh: aToB, closeCh: doneA}
	b := &pipePacketEndpoint{inCh: aToB, outCh: bToA, closeCh: doneB}
	return a, b
}

func (p *pipePacketEndpoint) closeStream() {
	p.closeOnce.Do(func() {
		close(p.closeCh)
	})
}

func (p *pipePacketEndpoint) RecvPacket() ([]byte, *pb.TunnelClose, error) {
	select {
	case <-p.closeCh:
		return nil, nil, io.EOF
	case pkt := <-p.inCh:
		if pkt.close != nil {
			return nil, pkt.close, nil
		}
		return pkt.data, nil, nil
	}
}

func (p *pipePacketEndpoint) SendRawData(data []byte) error {
	select {
	case <-p.closeCh:
		return io.ErrClosedPipe
	case p.outCh <- packetOrClose{data: data}:
		return nil
	}
}

func (p *pipePacketEndpoint) SendClose(closeMsg *pb.TunnelClose) error {
	select {
	case <-p.closeCh:
		return io.ErrClosedPipe
	case p.outCh <- packetOrClose{close: closeMsg}:
		return nil
	}
}

func TestBidiStreamConn_HTTP2BidirectionalAndTrailers(t *testing.T) {
	serverEp, clientEp := newPacketPipePair()
	defer serverEp.closeStream()
	defer clientEp.closeStream()

	serverConn := newBidiStreamConn(serverEp)
	clientConn := newBidiStreamConn(clientEp)
	defer func() { _ = serverConn.Close() }()
	defer func() { _ = clientConn.Close() }()

	// Peer runs stdlib h2c http.Server over clientConn
	go func() {
		serveTunnelHTTP2(clientConn, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/sse" {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				fl, _ := w.(http.Flusher)
				for i := 0; i < 3; i++ {
					_, _ = fmt.Fprintf(w, "data: chunk-%d\n\n", i)
					if fl != nil {
						fl.Flush()
					}
					time.Sleep(15 * time.Millisecond)
				}
				return
			}
			w.Header().Add("Trailer", "Grpc-Status")
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("X-Method", r.Method)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(bytes.ToUpper(body))
			w.Header().Set("Grpc-Status", "0")
		}))
	}()

	cc, err := newTunnelHTTP2ClientConn(context.Background(), serverConn, 5*time.Second, 5*time.Second)
	require.NoError(t, err)
	defer func() { _ = cc.Close() }()

	targetURL, _ := url.Parse("http://tunnel.local")
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(targetURL)
		},
		Transport:     &headerTimeoutRoundTripper{base: cc, timeout: 2 * time.Second, idleTimeout: 2 * time.Second},
		FlushInterval: -1,
	}
	proxySrv := httptest.NewUnstartedServer(proxy)
	proxySrv.EnableHTTP2 = true
	proxySrv.StartTLS()
	defer proxySrv.Close()

	// 1. POST echo + trailers
	resp, err := proxySrv.Client().Post(proxySrv.URL+"/echo", "text/plain", strings.NewReader("hello world"))
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "HELLO WORLD", string(body))
	assert.Equal(t, "0", resp.Trailer.Get("Grpc-Status"))

	// 2. SSE streaming flushes each chunk promptly
	sseResp, err := proxySrv.Client().Get(proxySrv.URL + "/sse")
	require.NoError(t, err)
	defer func() { _ = sseResp.Body.Close() }()
	buf := make([]byte, 256)
	start := time.Now()
	n, err := sseResp.Body.Read(buf)
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 100*time.Millisecond)
	assert.Contains(t, string(buf[:n]), "data: chunk-0")
	rest, err := io.ReadAll(sseResp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(rest), "data: chunk-2")
}

func TestBidiStreamConn_RemoteCloseOutOfBand(t *testing.T) {
	serverEp, clientEp := newPacketPipePair()
	defer serverEp.closeStream()
	defer clientEp.closeStream()

	clientConn := newBidiStreamConn(clientEp)
	defer func() { _ = clientConn.Close() }()

	require.NoError(t, serverEp.SendClose(&pb.TunnelClose{
		Code:   pb.CloseCode_CLOSE_CODE_EVICTED,
		Reason: "replaced by newer tunnel",
	}))

	require.True(t, clientConn.WaitDone(time.Second))
	evt, ok := clientConn.RemoteClose()
	require.True(t, ok)
	assert.Equal(t, pb.CloseCode_CLOSE_CODE_EVICTED, evt.Code)
	assert.Equal(t, "replaced by newer tunnel", evt.Reason)

	buf := make([]byte, 16)
	_, err := clientConn.Read(buf)
	assert.ErrorIs(t, err, io.EOF)
}

func TestBidiStreamConn_SlowStreamDoesNotBlockSiblingOrPing(t *testing.T) {
	serverEp, clientEp := newPacketPipePair()
	defer serverEp.closeStream()
	defer clientEp.closeStream()

	serverConn := newBidiStreamConn(serverEp)
	clientConn := newBidiStreamConn(clientEp)
	defer func() { _ = serverConn.Close() }()
	defer func() { _ = clientConn.Close() }()

	slowStarted := make(chan struct{})
	slowRelease := make(chan struct{})
	go func() {
		serveTunnelHTTP2(clientConn, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/slow" {
				w.WriteHeader(http.StatusOK)
				if fl, ok := w.(http.Flusher); ok {
					_, _ = w.Write([]byte("start"))
					fl.Flush()
				}
				close(slowStarted)
				_, _ = w.Write(bytes.Repeat([]byte("x"), 512<<10))
				<-slowRelease
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("fast-ok"))
		}))
	}()

	cc, err := newTunnelHTTP2ClientConn(context.Background(), serverConn, 50*time.Millisecond, 500*time.Millisecond)
	require.NoError(t, err)
	defer func() { _ = cc.Close() }()

	reqSlow, _ := http.NewRequest(http.MethodGet, "http://tunnel.local/slow", nil)
	slowResp, err := cc.RoundTrip(reqSlow)
	require.NoError(t, err)
	defer func() { _ = slowResp.Body.Close() }()
	<-slowStarted

	// Do not read slowResp.Body; sibling stream and idle HTTP/2 PING health checks must still succeed rapidly.
	reqFast, _ := http.NewRequest(http.MethodGet, "http://tunnel.local/fast", nil)
	fastCtx, cancelFast := context.WithTimeout(context.Background(), time.Second)
	defer cancelFast()
	fastResp, err := cc.RoundTrip(reqFast.WithContext(fastCtx))
	require.NoError(t, err)
	fastBody, err := io.ReadAll(fastResp.Body)
	_ = fastResp.Body.Close()
	require.NoError(t, err)
	assert.Equal(t, "fast-ok", string(fastBody))

	// Wait past SendPingTimeout (50ms) and verify connection remains healthy on a sibling stream.
	time.Sleep(120 * time.Millisecond)
	require.NoError(t, cc.Err())
	reqFast2, _ := http.NewRequest(http.MethodGet, "http://tunnel.local/fast", nil)
	fastCtx2, cancelFast2 := context.WithTimeout(context.Background(), time.Second)
	defer cancelFast2()
	fastResp2, err := cc.RoundTrip(reqFast2.WithContext(fastCtx2))
	require.NoError(t, err)
	fastBody2, err := io.ReadAll(fastResp2.Body)
	_ = fastResp2.Body.Close()
	require.NoError(t, err)
	assert.Equal(t, "fast-ok", string(fastBody2))
	close(slowRelease)
}

func TestHeaderAndIdleTimeouts(t *testing.T) {
	t.Run("header timeout fires when headers stall", func(t *testing.T) {
		serverEp, clientEp := newPacketPipePair()
		defer serverEp.closeStream()
		defer clientEp.closeStream()

		serverConn := newBidiStreamConn(serverEp)
		clientConn := newBidiStreamConn(clientEp)
		defer func() { _ = serverConn.Close() }()
		defer func() { _ = clientConn.Close() }()

		release := make(chan struct{})
		defer close(release)
		go func() {
			serveTunnelHTTP2(clientConn, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
		}()

		cc, err := newTunnelHTTP2ClientConn(context.Background(), serverConn, 0, 0)
		require.NoError(t, err)
		defer func() { _ = cc.Close() }()

		rt := &headerTimeoutRoundTripper{base: cc, timeout: 50 * time.Millisecond, idleTimeout: time.Second}
		req, _ := http.NewRequest(http.MethodGet, "http://tunnel.local/stall-hdr", nil)
		_, err = rt.RoundTrip(req)
		require.Error(t, err)
		assert.ErrorIs(t, err, errResponseHeaderTimeout)
	})

	t.Run("trickling stream outlives header timeout while stalled body hits idle timeout", func(t *testing.T) {
		serverEp, clientEp := newPacketPipePair()
		defer serverEp.closeStream()
		defer clientEp.closeStream()

		serverConn := newBidiStreamConn(serverEp)
		clientConn := newBidiStreamConn(clientEp)
		defer func() { _ = serverConn.Close() }()
		defer func() { _ = clientConn.Close() }()

		go func() {
			serveTunnelHTTP2(clientConn, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fl, _ := w.(http.Flusher)
				if r.URL.Path == "/trickle" {
					w.WriteHeader(http.StatusOK)
					for i := 0; i < 4; i++ {
						_, _ = fmt.Fprintf(w, "%d", i)
						if fl != nil {
							fl.Flush()
						}
						time.Sleep(25 * time.Millisecond)
					}
					return
				}
				w.WriteHeader(http.StatusOK)
				if fl != nil {
					fl.Flush()
				}
				<-r.Context().Done()
			}))
		}()

		cc, err := newTunnelHTTP2ClientConn(context.Background(), serverConn, 0, 0)
		require.NoError(t, err)
		defer func() { _ = cc.Close() }()

		rt := &headerTimeoutRoundTripper{base: cc, timeout: 60 * time.Millisecond, idleTimeout: 80 * time.Millisecond}

		// Trickle sends 4 chunks x 25ms = 100ms total (> 60ms header timeout & > 80ms idle timeout total)
		reqTrickle, _ := http.NewRequest(http.MethodGet, "http://tunnel.local/trickle", nil)
		resp, err := rt.RoundTrip(reqTrickle)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.NoError(t, err)
		assert.Equal(t, "0123", string(body))

		// Stalled body stalls for >80ms after headers arrive
		reqStall, _ := http.NewRequest(http.MethodGet, "http://tunnel.local/stall-body", nil)
		stallResp, err := rt.RoundTrip(reqStall)
		require.NoError(t, err)
		_, err = io.ReadAll(stallResp.Body)
		_ = stallResp.Body.Close()
		require.Error(t, err)
		assert.True(t, errors.Is(err, errStreamIdleTimeout))
	})

	t.Run("header timeout does not fire while request body is actively uploading", func(t *testing.T) {
		serverEp, clientEp := newPacketPipePair()
		defer serverEp.closeStream()
		defer clientEp.closeStream()

		serverConn := newBidiStreamConn(serverEp)
		clientConn := newBidiStreamConn(clientEp)
		defer func() { _ = serverConn.Close() }()
		defer func() { _ = clientConn.Close() }()

		go func() {
			serveTunnelHTTP2(clientConn, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(b)
			}))
		}()

		cc, err := newTunnelHTTP2ClientConn(context.Background(), serverConn, 0, 0)
		require.NoError(t, err)
		defer func() { _ = cc.Close() }()

		rt := &headerTimeoutRoundTripper{base: cc, timeout: 50 * time.Millisecond, idleTimeout: time.Second}
		pr, pw := io.Pipe()
		go func() {
			_, _ = pw.Write([]byte("part1-"))
			time.Sleep(80 * time.Millisecond) // Longer than header timeout, still uploading body
			_, _ = pw.Write([]byte("part2"))
			_ = pw.Close()
		}()

		req, _ := http.NewRequest(http.MethodPost, "http://tunnel.local/upload", pr)
		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.NoError(t, err)
		assert.Equal(t, "part1-part2", string(body))
	})
}

type blockedSendStream struct {
	unblock chan struct{}
	once    sync.Once
}

func (s *blockedSendStream) RecvPacket() ([]byte, *pb.TunnelClose, error) {
	<-s.unblock
	return nil, nil, io.EOF
}

func (s *blockedSendStream) SendRawData([]byte) error {
	<-s.unblock
	return io.ErrClosedPipe
}

func (s *blockedSendStream) SendClose(*pb.TunnelClose) error {
	<-s.unblock
	return io.ErrClosedPipe
}

func (s *blockedSendStream) release() {
	s.once.Do(func() { close(s.unblock) })
}

func TestBidiStreamConn_CloseInterruptsBlockedSendAndFiresOnClose(t *testing.T) {
	st := &blockedSendStream{unblock: make(chan struct{})}
	defer st.release()

	conn := newBidiStreamConn(st)
	var onCloseCalled atomic.Bool
	conn.SetOnClose(func() { onCloseCalled.Store(true) })

	// Fill sendCh so Write enters blocked send select
	writeBlocked := make(chan struct{})
	writeErrCh := make(chan error, 1)
	go func() {
		payload := bytes.Repeat([]byte("a"), maxTunnelRawFrameSize)
		for i := 0; i < 66; i++ {
			if i == 65 {
				close(writeBlocked)
			}
			if _, err := conn.Write(payload); err != nil {
				writeErrCh <- err
				return
			}
		}
		writeErrCh <- nil
	}()

	<-writeBlocked
	start := time.Now()
	require.NoError(t, conn.Close())
	assert.Less(t, time.Since(start), 50*time.Millisecond, "Close must be lock-free")
	assert.True(t, onCloseCalled.Load())

	select {
	case err := <-writeErrCh:
		assert.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(time.Second):
		t.Fatal("Write did not unblock after Close()")
	}

	// SendCloseContext must respect short context deadline even while writer goroutine is wedged in stream.Send
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	closeErr := conn.SendCloseContext(ctx, pb.CloseCode_CLOSE_CODE_SERVER_SHUTDOWN, "shutdown")
	assert.ErrorIs(t, closeErr, context.DeadlineExceeded)
	conn.CloseStream()
}
