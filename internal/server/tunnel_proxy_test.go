package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTunnelProxy(t *testing.T) {
	fx := setupTunnelTestFixture(t)
	srv := NewTunnelProxyServer(fx.tm, 18081, slog.Default())
	require.NotNil(t, srv)
	assert.Equal(t, ":18081", srv.Addr)
	assert.NotNil(t, srv.Handler)
	assert.Equal(t, 10*time.Second, srv.ReadHeaderTimeout)
	assert.Zero(t, srv.ReadTimeout, "ReadTimeout must be unset so long-lived uploads/streams are not aborted")
	assert.Zero(t, srv.WriteTimeout, "WriteTimeout must be unset so SSE/streaming responses are not killed at 60s")
	assert.Equal(t, 120*time.Second, srv.IdleTimeout)
	require.NotNil(t, srv.Protocols)
	assert.True(t, srv.Protocols.HTTP1())
	assert.True(t, srv.Protocols.UnencryptedHTTP2())
}

func TestTunnelProxy_OptionalAuthToken(t *testing.T) {
	tm := NewTunnelManagerWithConfig(TunnelManagerConfig{
		ProxyAuthToken: "secret-proxy-token",
	})
	srv := NewTunnelProxyServer(tm, 18081, slog.Default())

	// Missing token -> 401
	reqMissing := httptest.NewRequest(http.MethodGet, "/", nil)
	reqMissing.Header.Set("X-Diverge-Env", "demo")
	recMissing := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recMissing, reqMissing)
	assert.Equal(t, http.StatusUnauthorized, recMissing.Code)

	// Wrong token -> 401
	reqWrong := httptest.NewRequest(http.MethodGet, "/", nil)
	reqWrong.Header.Set("X-Diverge-Env", "demo")
	reqWrong.Header.Set("X-Diverge-Proxy-Token", "wrong")
	recWrong := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recWrong, reqWrong)
	assert.Equal(t, http.StatusUnauthorized, recWrong.Code)

	// Valid token passes auth middleware and strips token before hitting handler -> reaches 502 (no active tunnel)
	reqValid := httptest.NewRequest(http.MethodGet, "/", nil)
	reqValid.Header.Set("X-Diverge-Env", "demo")
	reqValid.Header.Set("X-Diverge-Proxy-Token", "secret-proxy-token")
	recValid := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recValid, reqValid)
	assert.Equal(t, http.StatusBadGateway, recValid.Code)
	assert.Empty(t, reqValid.Header.Get("X-Diverge-Proxy-Token"), "X-Diverge-Proxy-Token must be stripped before proxying")

	// Duplicate X-Diverge-Proxy-Token headers must be rejected even if the first matches.
	reqDup := httptest.NewRequest(http.MethodGet, "/", nil)
	reqDup.Header.Set("X-Diverge-Env", "demo")
	reqDup.Header.Add("X-Diverge-Proxy-Token", "secret-proxy-token")
	reqDup.Header.Add("X-Diverge-Proxy-Token", "attacker-extra")
	recDup := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recDup, reqDup)
	assert.Equal(t, http.StatusUnauthorized, recDup.Code)

	// Whitespace-padded configured token is trimmed; whitespace-only disables middleware.
	tmTrim := NewTunnelManagerWithConfig(TunnelManagerConfig{
		ProxyAuthToken: "  secret-proxy-token\n",
	})
	srvTrim := NewTunnelProxyServer(tmTrim, 18081, slog.Default())
	reqTrim := httptest.NewRequest(http.MethodGet, "/", nil)
	reqTrim.Header.Set("X-Diverge-Env", "demo")
	reqTrim.Header.Set("X-Diverge-Proxy-Token", "secret-proxy-token")
	recTrim := httptest.NewRecorder()
	srvTrim.Handler.ServeHTTP(recTrim, reqTrim)
	assert.Equal(t, http.StatusBadGateway, recTrim.Code)

	tmBlank := NewTunnelManagerWithConfig(TunnelManagerConfig{
		ProxyAuthToken: "   \n",
	})
	srvBlank := NewTunnelProxyServer(tmBlank, 18081, slog.Default())
	reqBlank := httptest.NewRequest(http.MethodGet, "/", nil)
	reqBlank.Header.Set("X-Diverge-Env", "demo")
	recBlank := httptest.NewRecorder()
	srvBlank.Handler.ServeHTTP(recBlank, reqBlank)
	assert.Equal(t, http.StatusBadGateway, recBlank.Code)
}
