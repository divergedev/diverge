package server

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
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

// hasTunnelProxyRoutingIntent reports whether an HTTP request on :8081 carries
// explicit tunnel-proxy routing headers or targets a per-tunnel Kubernetes Service Host.
// Requests with routing intent must always enter the data-plane tunnel proxy so
// external preview traffic cannot invoke control-plane PreviewResolverService paths.
func hasTunnelProxyRoutingIntent(r *http.Request, proxyServiceAddr string) bool {
	if len(r.Header.Values("X-Diverge-Env")) > 0 ||
		len(r.Header.Values("X-Diverge-Namespace")) > 0 ||
		len(r.Header.Values("X-Diverge-Service")) > 0 {
		return true
	}

	hostSvc, _, _ := extractServicePreviewAndNamespaceFromHost(r.Host)
	if strings.HasPrefix(hostSvc, "diverge-tsvc-") {
		return true
	}
	if strings.HasPrefix(hostSvc, "diverge-tunnel-") {
		centralLabel := strings.TrimSpace(proxyServiceAddr)
		centralLabel = strings.TrimPrefix(centralLabel, "https://")
		centralLabel = strings.TrimPrefix(centralLabel, "http://")
		if idx := strings.IndexByte(centralLabel, ':'); idx != -1 {
			centralLabel = centralLabel[:idx]
		}
		if dotIdx := strings.IndexByte(centralLabel, '.'); dotIdx != -1 {
			centralLabel = centralLabel[:dotIdx]
		}
		return hostSvc != "diverge-tunnel-proxy" && (centralLabel == "" || hostSvc != centralLabel)
	}
	return false
}

// NewTunnelProxyServer creates the dedicated HTTP/1.1 + h2c server for the cluster-internal tunnel proxy (:8081).
func NewTunnelProxyServer(tm *TunnelManager, port int, logger *slog.Logger) *http.Server {
	if logger == nil {
		logger = slog.Default()
	}
	mux := http.NewServeMux()
	if tm != nil {
		resolvePath, resolveHandler := divergev1alpha1connect.NewPreviewResolverServiceHandler(
			tm,
			connect.WithInterceptors(NewMetricsInterceptor()),
		)
		proxyHandler := tm.NewTunnelProxyHandler()
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !hasTunnelProxyRoutingIntent(r, tm.proxyServiceAddr) && strings.HasPrefix(r.URL.Path, resolvePath) {
				resolveHandler.ServeHTTP(w, r)
				return
			}
			proxyHandler.ServeHTTP(w, r)
		}))
	} else {
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "tunnel manager unavailable", http.StatusServiceUnavailable)
		}))
	}

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
