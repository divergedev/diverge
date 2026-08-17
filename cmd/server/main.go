package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/cors"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	divergev1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/server"
	"github.com/divergedev/diverge/internal/server/auth"
	"github.com/divergedev/diverge/internal/server/streaming"
)

var (
	scheme  = runtime.NewScheme()
	version = "dev" // Overridden via ldflags: -X main.version=...
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(divergev1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		addr               string
		metricsAddr        string
		tlsCertFile        string
		tlsKeyFile         string
		tokenCacheTTL      time.Duration
		maxStreams         int
		audiences          string
		corsAllowedOrigins string
		corsMaxAge         int
	)

	flag.StringVar(&addr, "addr", ":8443", "Main server listen address")
	flag.StringVar(&metricsAddr, "metrics-addr", ":9090", "Prometheus metrics endpoint address")
	flag.StringVar(&tlsCertFile, "tls-cert-file", "", "TLS certificate file (optional)")
	flag.StringVar(&tlsKeyFile, "tls-key-file", "", "TLS private key file (optional)")
	flag.DurationVar(&tokenCacheTTL, "token-cache-ttl", 5*time.Second, "TokenReview cache TTL")
	flag.IntVar(&maxStreams, "max-streams", 1000, "Maximum concurrent streams")
	flag.StringVar(&audiences, "audiences", "diverge-server", "Comma-separated list of valid token audiences")
	// WARNING: Default "*" allows all origins. In production, set this to your
	// specific domain(s) to prevent unauthorized cross-origin access.
	flag.StringVar(&corsAllowedOrigins, "cors-allowed-origins", "*", "Comma-separated list of allowed CORS origins")
	flag.IntVar(&corsMaxAge, "cors-max-age", 86400, "CORS max age in seconds")
	flag.Parse()

	// Structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting diverge-server",
		"addr", addr,
		"metrics_addr", metricsAddr,
		"token_cache_ttl", tokenCacheTTL,
		"max_streams", maxStreams,
	)

	// Build in-cluster K8s config
	cfg := ctrl.GetConfigOrDie()

	// Create typed clientset (for TokenReview, SubjectAccessReview)
	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logger.Error("failed to create kubernetes clientset", "error", err)
		os.Exit(1)
	}

	// Create controller-runtime client (for CRD operations).
	// We intentionally use client.New() (direct, uncached client) instead of
	// a cached client to avoid read-your-writes cache lag. This ensures API
	// clients always read the most up-to-date state immediately after mutations.
	crClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		logger.Error("failed to create controller-runtime client", "error", err)
		os.Exit(1)
	}

	// Initialize streaming components
	broadcasterMetrics := server.GetBroadcasterMetrics()
	informerMgr := streaming.NewInformerManager(logger, broadcasterMetrics)
	logStreamer := streaming.NewLogStreamer(k8sClient)

	// Create stream semaphore (injected, not global)
	streamSemaphore := make(chan struct{}, maxStreams)

	// Auth setup
	tokenCache := auth.NewTokenCache(1024, tokenCacheTTL)
	authProvider := auth.NewTokenReviewProvider(k8sClient, []string{audiences})

	authMetrics := server.NewAuthMetrics()
	auditLogger := server.NewAuditLogger(logger)

	authMiddleware := auth.NewMiddleware(auth.MiddlewareConfig{
		Provider: authProvider,
		Cache:    tokenCache,
		Logger:   logger,
		Metrics: &auth.AuthMetrics{
			Latency:     authMetrics.Latency,
			CacheHits:   authMetrics.CacheHits,
			CacheMisses: authMetrics.CacheMisses,
			Attempts:    authMetrics.Attempts,
		},
		ExemptPaths: []string{"/healthz", "/readyz"},
	})

	// Build the ConnectRPC mux
	mux := server.NewServeMux(server.ServeMuxConfig{
		Client:          crClient,
		K8sClient:       k8sClient,
		InformerMgr:     informerMgr,
		LogStreamer:     logStreamer,
		StreamSemaphore: streamSemaphore,
		Logger:          logger,
		AuditLogger:     auditLogger,
		Version:         version,
	})

	// Health check (exempt from auth)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	// Wrap with auth middleware
	handler := authMiddleware(mux)

	rawOrigins := strings.Split(corsAllowedOrigins, ",")
	origins := make([]string, 0, len(rawOrigins))
	for _, o := range rawOrigins {
		trimmed := strings.TrimSpace(o)
		if trimmed != "" {
			origins = append(origins, trimmed)
		}
	}

	opts := cors.Options{
		AllowedMethods:   []string{"GET", "POST"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "Connect-Protocol-Version", "Connect-Timeout-Ms", "X-Grpc-Web", "X-User-Agent", "Grpc-Timeout"},
		ExposedHeaders:   []string{"Grpc-Status", "Grpc-Message", "Grpc-Status-Details-Bin"},
		AllowCredentials: true,
		MaxAge:           corsMaxAge,
	}
	if len(origins) == 1 && origins[0] == "*" {
		opts.AllowOriginFunc = func(origin string) bool { return true }
	} else {
		opts.AllowedOrigins = origins
	}

	corsHandler := cors.New(opts)

	handler = corsHandler.Handler(handler)

	// Signal-driven graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Main server — do NOT bind signal context to BaseContext
	// as it would cancel in-flight requests immediately on SIGTERM,
	// bypassing graceful shutdown. Let Shutdown() handle draining.
	mainSrv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Metrics server
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.HandlerFor(crmetrics.Registry, promhttp.HandlerOpts{}))
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	metricsSrv := &http.Server{
		Addr:              metricsAddr,
		Handler:           metricsMux,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Use errgroup to tie server lifecycles together
	g, gCtx := errgroup.WithContext(ctx)

	// Start main server
	g.Go(func() error {
		logger.Info("server listening", "addr", addr)
		var listenErr error
		if tlsCertFile != "" && tlsKeyFile != "" {
			listenErr = mainSrv.ListenAndServeTLS(tlsCertFile, tlsKeyFile)
		} else {
			listenErr = mainSrv.ListenAndServe()
		}
		if listenErr != nil && listenErr != http.ErrServerClosed {
			return fmt.Errorf("server listen failed: %w", listenErr)
		}
		return nil
	})

	// Start metrics server
	g.Go(func() error {
		logger.Info("metrics server listening", "addr", metricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("metrics server listen failed: %w", err)
		}
		return nil
	})

	// Graceful shutdown goroutine
	g.Go(func() error {
		<-gCtx.Done()
		logger.Info("shutdown signal received, draining connections")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		// Shutdown main server (drains in-flight requests)
		if err := mainSrv.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown error", "error", err)
		}
		// Shutdown metrics server
		if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
			logger.Error("metrics server shutdown error", "error", err)
		}
		logger.Info("shutdown complete")
		return nil
	})

	if err := g.Wait(); err != nil {
		logger.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}
