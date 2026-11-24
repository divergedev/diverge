package server

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// tunnelProxyAuthMiddleware enforces an optional shared secret on :8081 cluster-internal requests.
func tunnelProxyAuthMiddleware(expectedToken string, logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	expected := []byte(strings.TrimSpace(expectedToken))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vals := r.Header.Values("X-Diverge-Proxy-Token")
		if len(vals) != 1 || subtle.ConstantTimeCompare([]byte(vals[0]), expected) != 1 {
			logger.Warn("tunnel proxy request rejected: invalid or duplicate proxy token",
				"remote_addr", r.RemoteAddr,
				"method", r.Method,
				"path", r.URL.Path,
				"header_count", len(vals),
			)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Header.Del("X-Diverge-Proxy-Token")
		next.ServeHTTP(w, r)
	})
}

// NewTunnelProxyServer creates the dedicated HTTP/1.1 + h2c server for the cluster-internal tunnel proxy (:8081).
func NewTunnelProxyServer(tm *TunnelManager, port int, logger *slog.Logger) *http.Server {
	if logger == nil {
		logger = slog.Default()
	}
	mux := http.NewServeMux()
	mux.Handle("/", tm.NewTunnelProxyHandler())

	var handler http.Handler = mux
	if tm != nil {
		if token := strings.TrimSpace(tm.ProxyAuthToken()); token != "" {
			handler = tunnelProxyAuthMiddleware(token, logger, mux)
		}
	}

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           handler,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		Protocols:         protocols,
	}
}

// ListenAndServeTunnelProxy starts the tunnel proxy server on the given port.
func ListenAndServeTunnelProxy(tm *TunnelManager, port int, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	srv := NewTunnelProxyServer(tm, port, logger)
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on tunnel proxy port %d: %w", port, err)
	}
	logger.Info("tunnel proxy server started", "port", port)
	return srv.Serve(ln)
}
