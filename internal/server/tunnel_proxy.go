package server

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
)

// NewTunnelProxyServer creates a dedicated HTTP server for the tunnel proxy.
// P0 #3: This runs on a SEPARATE port from the RPC server.
// P0 #6: NO auth middleware — cluster-internal traffic only.
// Host-header validation prevents open proxy abuse.
func NewTunnelProxyServer(tm *TunnelManager, port int, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/", tm.NewTunnelProxyHandler())

	return &http.Server{
		Addr:     fmt.Sprintf(":%d", port),
		Handler:  mux,
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
}

// ListenAndServeTunnelProxy starts the tunnel proxy server on the given port.
func ListenAndServeTunnelProxy(tm *TunnelManager, port int, logger *slog.Logger) error {
	srv := NewTunnelProxyServer(tm, port, logger)
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on tunnel proxy port %d: %w", port, err)
	}
	logger.Info("tunnel proxy server started", "port", port)
	return srv.Serve(ln)
}
