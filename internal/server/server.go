package server

import (
	"log"
	"net/http"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/internal/server/streaming"
)

var StreamSemaphore = make(chan struct{}, 100)

func NewServeMux(c client.Client, informerMgr *streaming.InformerManager, logStreamer *streaming.LogStreamer) *http.ServeMux {
	mux := http.NewServeMux()

	interceptors := connect.WithInterceptors(
		NewMetricsInterceptor(),
		NewAuthInterceptor(authAttemptsTotal),
	)

	envService := NewEnvironmentService(c, informerMgr, logStreamer)
	envPath, envHandler := divergev1alpha1connect.NewEnvironmentServiceHandler(envService, interceptors)
	mux.Handle(envPath, envHandler)

	pgService := NewPreviewGroupService(c, informerMgr)
	pgPath, pgHandler := divergev1alpha1connect.NewPreviewGroupServiceHandler(pgService, interceptors)
	mux.Handle(pgPath, pgHandler)

	clusterService := NewClusterService()
	clusterPath, clusterHandler := divergev1alpha1connect.NewClusterServiceHandler(clusterService, interceptors)
	mux.Handle(clusterPath, clusterHandler)

	authService := NewAuthService()
	authPath, authHandler := divergev1alpha1connect.NewAuthServiceHandler(authService, interceptors)
	mux.Handle(authPath, authHandler)

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.HandlerFor(crmetrics.Registry, promhttp.HandlerOpts{}))
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	go func() {
		log.Printf("Metrics server listening on :9090")
		if err := http.ListenAndServe(":9090", metricsMux); err != nil && err != http.ErrServerClosed {
			log.Fatalf("metrics server failed: %v", err)
		}
	}()

	return mux
}
