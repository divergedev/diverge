package server

import (
	"log/slog"
	"net/http"

	"connectrpc.com/connect"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/internal/server/streaming"
)

// ServeMuxConfig holds all dependencies for the ConnectRPC server mux.
type ServeMuxConfig struct {
	Client        client.Client
	K8sClient     kubernetes.Interface
	InformerMgr   *streaming.InformerManager
	LogStreamer   *streaming.LogStreamer
	StreamLimiter *StreamLimiter
	Logger        *slog.Logger
	AuditLogger   *AuditLogger
	Version       string
}

// NewServeMux creates the ConnectRPC service mux with all handlers registered.
// Auth is NOT applied here — it is applied at the net/http middleware layer.
func NewServeMux(cfg ServeMuxConfig) (*http.ServeMux, *TunnelManager) {
	// Default nil dependencies to prevent panics
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.AuditLogger == nil {
		cfg.AuditLogger = NewAuditLogger(cfg.Logger)
	}
	if cfg.StreamLimiter == nil {
		cfg.StreamLimiter = NewStreamLimiter(250, 20) // Sensible defaults for medium teams
	}

	mux := http.NewServeMux()

	interceptors := connect.WithInterceptors(
		NewMetricsInterceptor(),
	)

	envService := NewEnvironmentService(cfg.Client, cfg.K8sClient, cfg.InformerMgr, cfg.LogStreamer, cfg.StreamLimiter, cfg.Logger, cfg.AuditLogger)
	envPath, envHandler := divergev1alpha1connect.NewEnvironmentServiceHandler(envService, interceptors)
	mux.Handle(envPath, envHandler)

	pgService := NewPreviewGroupService(cfg.Client, cfg.K8sClient, cfg.InformerMgr, cfg.StreamLimiter, cfg.Logger, cfg.AuditLogger)
	pgPath, pgHandler := divergev1alpha1connect.NewPreviewGroupServiceHandler(pgService, interceptors)
	mux.Handle(pgPath, pgHandler)

	clusterService := NewClusterService(cfg.Client, cfg.K8sClient, cfg.Logger, cfg.AuditLogger, cfg.Version)
	clusterPath, clusterHandler := divergev1alpha1connect.NewClusterServiceHandler(clusterService, interceptors)
	mux.Handle(clusterPath, clusterHandler)

	authService := NewAuthService(cfg.K8sClient, cfg.Logger, cfg.AuditLogger)
	authPath, authHandler := divergev1alpha1connect.NewAuthServiceHandler(authService, interceptors)
	mux.Handle(authPath, authHandler)

	tunnelMgr := NewTunnelManager(cfg.Client, cfg.K8sClient, cfg.Logger, cfg.AuditLogger)
	tunnelPath, tunnelHandler := divergev1alpha1connect.NewTunnelServiceHandler(tunnelMgr, interceptors)
	mux.Handle(tunnelPath, tunnelHandler)
	// NOTE: Tunnel proxy handler is on a SEPARATE port (8081), not this mux.
	// See NewTunnelProxyServer() for the dedicated proxy listener.

	return mux, tunnelMgr
}
