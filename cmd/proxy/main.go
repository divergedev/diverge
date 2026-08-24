package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/proxy"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	scheme = runtime.NewScheme()

	// Set via ldflags: -X main.version=... -X main.commit=... -X main.date=...
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	logger := ctrl.Log.WithName("proxy")

	// Version fallback for go install users
	if version == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
			version = bi.Main.Version
		}
	}
	logger.Info("starting diverge-proxy", "version", version, "commit", commit, "date", date)

	var (
		port          int
		previewDomain string
		upstream      string
		headerKey     string
		kubeconfig    string
		namespace     string
	)

	flag.IntVar(&port, "port", 8090, "Port to listen on")
	flag.StringVar(&previewDomain, "preview-domain", "", "Wildcard domain (e.g., preview.example.com)")
	flag.StringVar(&upstream, "upstream", "", "Upstream base URL (e.g., https://app.staging.example.com)")
	flag.StringVar(&headerKey, "header-key", "x-diverge-env", "Header to inject")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig")
	flag.StringVar(&namespace, "namespace", "default", "Kubernetes namespace")
	flag.Parse()

	if previewDomain == "" || upstream == "" {
		fmt.Fprintln(os.Stderr, "--preview-domain and --upstream are required")
		os.Exit(1)
	}

	cfg := proxy.Config{
		BaseURL:       upstream,
		HeaderKey:     headerKey,
		PreviewDomain: previewDomain,
		Port:          port,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	lister, err := proxy.NewK8sEnvironmentLister(ctx, kubeconfig, namespace, scheme)
	if err != nil {
		logger.Error(err, "Failed to initialize K8s client")
		os.Exit(1)
	}

	server, err := proxy.NewServer(cfg, lister)
	if err != nil {
		logger.Error(err, "Failed to initialize proxy server")
		os.Exit(1)
	}

	handler := proxy.LoggingMiddleware(proxy.CORSMiddleware(previewDomain, server))
	addr := fmt.Sprintf(":%d", port)

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error(err, "server failed")
			os.Exit(1)
		}
	}()

	logger.Info("proxy started", "addr", addr, "previewDomain", previewDomain, "upstream", upstream)
	<-ctx.Done()
	logger.Info("shutting down, draining in-flight requests...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error(err, "forced shutdown")
		os.Exit(1)
	}
	logger.Info("proxy stopped")
}
