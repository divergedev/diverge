package tunnelclient

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
)

type packetOrClose struct {
	data  []byte
	close *pb.TunnelClose
}

type pipePacketEndpoint struct {
	inCh      chan packetOrClose
	outCh     chan packetOrClose
	closeCh   chan struct{}
	closeOnce sync.Once
}

func newPacketPipePair() (*pipePacketEndpoint, *pipePacketEndpoint) {
	aToB := make(chan packetOrClose, 64)
	bToA := make(chan packetOrClose, 64)
	doneA := make(chan struct{})
	doneB := make(chan struct{})
	return &pipePacketEndpoint{inCh: bToA, outCh: aToB, closeCh: doneA},
		&pipePacketEndpoint{inCh: aToB, outCh: bToA, closeCh: doneB}
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
	case pkt, ok := <-p.inCh:
		if !ok {
			return nil, nil, io.EOF
		}
		return pkt.data, pkt.close, nil
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

// pipePeerNetConn adapts the server side of newPacketPipePair for http.Transport.NewClientConn.
type pipePeerNetConn struct {
	ep  *pipePacketEndpoint
	pr  *io.PipeReader
	pw  *io.PipeWriter
	end sync.Once
}

func newPipePeerNetConn(ep *pipePacketEndpoint) *pipePeerNetConn {
	pr, pw := io.Pipe()
	c := &pipePeerNetConn{ep: ep, pr: pr, pw: pw}
	go func() {
		for {
			data, closeMsg, err := ep.RecvPacket()
			if err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			if closeMsg != nil {
				_ = pw.CloseWithError(io.EOF)
				return
			}
			if _, wErr := pw.Write(data); wErr != nil {
				return
			}
		}
	}()
	return c
}

func (c *pipePeerNetConn) Read(p []byte) (int, error) { return c.pr.Read(p) }
func (c *pipePeerNetConn) Write(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	if err := c.ep.SendRawData(cp); err != nil {
		return 0, err
	}
	return len(p), nil
}
func (c *pipePeerNetConn) Close() error {
	c.end.Do(func() {
		c.ep.closeStream()
		_ = c.pw.CloseWithError(net.ErrClosed)
		_ = c.pr.CloseWithError(net.ErrClosed)
	})
	return nil
}
func (c *pipePeerNetConn) LocalAddr() net.Addr                { return pipeAddr("peer-local") }
func (c *pipePeerNetConn) RemoteAddr() net.Addr               { return pipeAddr("peer-remote") }
func (c *pipePeerNetConn) SetDeadline(_ time.Time) error      { return nil }
func (c *pipePeerNetConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *pipePeerNetConn) SetWriteDeadline(_ time.Time) error { return nil }

func dialPipeHTTP2ClientConn(ctx context.Context, conn net.Conn) (*http.ClientConn, error) {
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	var once sync.Once
	tr := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			var c net.Conn
			once.Do(func() { c = conn })
			if c == nil {
				return nil, io.EOF
			}
			return c, nil
		},
		Protocols: &protocols,
	}
	return tr.NewClientConn(ctx, "http", "tunnel.local:80")
}

func startTestV3ProxyPair(t *testing.T, localHost string, localPort int, retryTimeout time.Duration) (*http.ClientConn, *pipePacketEndpoint, *bidiStreamConn, func()) {
	t.Helper()
	clientEp, serverEp := newPacketPipePair()
	bidi := newBidiStreamConn(clientEp)

	rp, err := newLocalReverseProxy(localHost, localPort, retryTimeout, nil, slog.Default())
	require.NoError(t, err)

	srv := newHTTP2TunnelServer(rp, slog.Default())
	ln := newSingleConnListener(bidi)
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = srv.Serve(ln)
	}()

	peerConn := newPipePeerNetConn(serverEp)
	cc, err := dialPipeHTTP2ClientConn(t.Context(), peerConn)
	require.NoError(t, err)

	cleanup := func() {
		_ = cc.Close()
		_ = peerConn.Close()
		bidi.CloseStream()
		_ = srv.Close()
		<-serveDone
	}
	return cc, serverEp, bidi, cleanup
}

func TestLocalReverseProxy_RoundTripHeadersAndSSE(t *testing.T) {
	localSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/headers":
			assert.Empty(t, r.Header.Get("X-Diverge-Env"))
			assert.Empty(t, r.Header.Get("X-Diverge-Proxy-Token"))
			assert.Equal(t, "preview.example.com", r.Header.Get("X-Forwarded-Host"))
			assert.Empty(t, r.URL.Host)
			_, _ = w.Write([]byte("host=" + r.Host))
		case "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			_, _ = w.Write([]byte("data: chunk1\n\n"))
			flusher.Flush()
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer localSrv.Close()

	_, portStr, err := net.SplitHostPort(localSrv.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	cc, _, _, cleanup := startTestV3ProxyPair(t, "127.0.0.1", port, 0)
	defer cleanup()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://tunnel.local/headers", nil)
	require.NoError(t, err)
	req.Host = "preview.example.com"
	req.Header.Set("X-Diverge-Env", "feat-1")
	req.Header.Set("X-Diverge-Proxy-Token", "secret")

	resp, err := cc.RoundTrip(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, fmt.Sprintf("host=127.0.0.1:%d", port), string(body))

	// Streaming SSE flush test.
	sseCtx, sseCancel := context.WithCancel(t.Context())
	defer sseCancel()
	sseReq, err := http.NewRequestWithContext(sseCtx, http.MethodGet, "http://tunnel.local/sse", nil)
	require.NoError(t, err)
	sseResp, err := cc.RoundTrip(sseReq)
	require.NoError(t, err)

	reader := bufio.NewReader(sseResp.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "data: chunk1\n", line)
	sseCancel()
	_ = sseResp.Body.Close()
}

func TestLocalReverseProxy_DoesNotFollowRedirectsSSRF(t *testing.T) {
	var trapHits atomic.Int32
	trapSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		trapHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer trapSrv.Close()

	localSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, trapSrv.URL+"/metadata", http.StatusFound)
	}))
	defer localSrv.Close()

	_, portStr, err := net.SplitHostPort(localSrv.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	cc, _, _, cleanup := startTestV3ProxyPair(t, "127.0.0.1", port, 0)
	defer cleanup()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://tunnel.local/ssrf", nil)
	require.NoError(t, err)
	resp, err := cc.RoundTrip(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, trapSrv.URL+"/metadata", resp.Header.Get("Location"))
	assert.Zero(t, trapHits.Load(), "tunnel reverse proxy must NEVER follow redirects from localhost")
}

func TestLocalReverseProxy_RejectsNonLoopbackHost(t *testing.T) {
	_, err := newLocalReverseProxy("169.254.169.254", 80, time.Second, nil, slog.Default())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loopback")
}

func TestLocalReverseProxy_ConnRefusedRetryRecovery(t *testing.T) {
	// Reserve an ephemeral port, close it, then re-bind after 250ms while proxy retries.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	_, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	require.NoError(t, ln.Close())

	cc, _, _, cleanup := startTestV3ProxyPair(t, "127.0.0.1", port, 2*time.Second)
	defer cleanup()

	var delayedSrv *httptest.Server
	var srvMu sync.Mutex
	go func() {
		time.Sleep(250 * time.Millisecond)
		newLn, lErr := net.Listen("tcp", addr)
		if lErr != nil {
			return
		}
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("recovered"))
		}))
		_ = srv.Listener.Close()
		srv.Listener = newLn
		srv.Start()
		srvMu.Lock()
		delayedSrv = srv
		srvMu.Unlock()
	}()
	defer func() {
		srvMu.Lock()
		if delayedSrv != nil {
			delayedSrv.Close()
		}
		srvMu.Unlock()
	}()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://tunnel.local/retry", nil)
	require.NoError(t, err)
	resp, err := cc.RoundTrip(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "recovered", string(body))
}

func TestBidiStreamConn_TunnelCloseAfterGOAWAY(t *testing.T) {
	clientEp, serverEp := newPacketPipePair()
	bidi := newBidiStreamConn(clientEp)
	defer func() {
		bidi.CloseStream()
		serverEp.closeStream()
	}()

	// Close the local pipe BEFORE the server sends TunnelClose, simulating HTTP/2 GOAWAY
	// closing the pipe slightly before the out-of-band TunnelClose frame arrives.
	_ = bidi.Close()

	require.NoError(t, serverEp.SendRawData([]byte("leftover-h2-goaway")))
	require.NoError(t, serverEp.SendClose(&pb.TunnelClose{
		Code:   pb.CloseCode_CLOSE_CODE_SERVER_SHUTDOWN,
		Reason: "server restarting",
	}))

	require.True(t, bidi.WaitDone(2*time.Second))
	evt, ok := bidi.RemoteClose()
	require.True(t, ok)
	assert.Equal(t, pb.CloseCode_CLOSE_CODE_SERVER_SHUTDOWN, evt.Code)
	assert.Equal(t, "server restarting", evt.Reason)
}

func TestLocalReverseProxy_ForwardedHeadersAndHostValidation(t *testing.T) {
	_, err := newLocalReverseProxy("127.0.0.1:9999", 8080, 0, nil, slog.Default())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a loopback address")

	_, err = newLocalReverseProxy("169.254.169.254", 8080, 0, nil, slog.Default())
	require.Error(t, err)

	var (
		gotHost string
		gotXFH  string
		gotXFP  string
		gotXFF  string
	)
	localSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotXFH = r.Header.Get("X-Forwarded-Host")
		gotXFP = r.Header.Get("X-Forwarded-Proto")
		gotXFF = r.Header.Get("X-Forwarded-For")
		w.WriteHeader(http.StatusOK)
	}))
	defer localSrv.Close()

	_, portStr, err := net.SplitHostPort(localSrv.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	proxy, err := newLocalReverseProxy("127.0.0.1", port, 0, nil, slog.Default())
	require.NoError(t, err)

	// 1. Existing inbound X-Forwarded-* headers are preserved.
	req := httptest.NewRequest(http.MethodGet, "http://preview.example.com/app", nil)
	req.Host = "internal.local"
	req.RemoteAddr = "198.51.100.2:1234"
	req.Header.Set("X-Forwarded-Host", "preview.example.com")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, fmt.Sprintf("127.0.0.1:%d", port), gotHost)
	assert.Equal(t, "preview.example.com", gotXFH)
	assert.Equal(t, "https", gotXFP)
	assert.Contains(t, gotXFF, "203.0.113.10")

	// 2. Missing X-Forwarded-Host falls back to inbound Request.Host.
	req2 := httptest.NewRequest(http.MethodGet, "http://fallback.example.com/app", nil)
	req2.Host = "fallback.example.com"
	req2.RemoteAddr = "198.51.100.3:5678"
	rec2 := httptest.NewRecorder()
	proxy.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)
	assert.Equal(t, "fallback.example.com", gotXFH)
}
