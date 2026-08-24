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

	"github.com/divergedev/diverge/internal/proxy"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Set via ldflags: -X main.version=... -X main.commit=... -X main.date=...
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	logger := ctrl.Log.WithName("activator-proxy")

	// Version fallback for go install users
	if version == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
			version = bi.Main.Version
		}
	}
	logger.Info("starting diverge-activator-proxy", "version", version, "commit", commit, "date", date)

	var (
		port            int
		activatorURL    string
		targetNamespace string
		targetSelector  string
		targetPort      int
		targetRevision  string
	)

	flag.IntVar(&port, "port", 8091, "Port to listen on")
	flag.StringVar(&activatorURL, "activator-url", "", "URL of the Knative activator service")
	flag.StringVar(&targetNamespace, "target-namespace", "default", "Namespace of the target Knative pods")
	flag.StringVar(&targetSelector, "target-selector", "", "Label selector to find target pods")
	flag.IntVar(&targetPort, "target-port", 8080, "Port of the target pod to proxy to")
	flag.StringVar(&targetRevision, "target-revision", "", "Knative revision name (auto-detected if empty)")
	flag.Parse()

	if activatorURL == "" || targetSelector == "" {
		fmt.Fprintln(os.Stderr, "--activator-url and --target-selector are required")
		os.Exit(1)
	}

	cfg := proxy.ActivatorProxyConfig{
		Port:            port,
		ActivatorURL:    activatorURL,
		TargetNamespace: targetNamespace,
		TargetSelector:  targetSelector,
		TargetPort:      targetPort,
		TargetRevision:  targetRevision,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	kubeConfig := ctrl.GetConfigOrDie()
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		logger.Error(err, "Failed to initialize K8s client")
		os.Exit(1)
	}

	server, err := proxy.NewActivatorServer(ctx, cfg, kubeClient)
	if err != nil {
		logger.Error(err, "Failed to initialize activator proxy server")
		os.Exit(1)
	}

	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           server,
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

	logger.Info("activator proxy started", "addr", addr, "activatorURL", activatorURL)
	<-ctx.Done()
	logger.Info("shutting down, draining in-flight requests...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error(err, "forced shutdown")
		os.Exit(1)
	}
	logger.Info("activator proxy stopped")
}
