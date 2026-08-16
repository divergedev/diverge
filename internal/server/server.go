package server

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/internal/server/streaming"
)

var StreamSemaphore = make(chan struct{}, 100)

func NewServeMux(c client.Client, informerMgr *streaming.InformerManager, logStreamer *streaming.LogStreamer) *http.ServeMux {
	mux := http.NewServeMux()

	interceptors := connect.WithInterceptors(
		NewMetricsInterceptor(),
		NewAuthInterceptor(),
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

	mux.Handle("/metrics", promhttp.Handler())

	return mux
}
