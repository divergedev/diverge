package server

import (
	"log/slog"
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
	assert.Equal(t, 30*time.Second, srv.ReadTimeout)
	assert.Equal(t, 60*time.Second, srv.WriteTimeout)
	assert.Equal(t, 120*time.Second, srv.IdleTimeout)
}
