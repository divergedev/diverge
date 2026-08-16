package server

import (
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
)

func NewServeMux(c client.Client) *http.ServeMux {
	mux := http.NewServeMux()

	envService := NewEnvironmentService(c)
	envPath, envHandler := divergev1alpha1connect.NewEnvironmentServiceHandler(envService)
	mux.Handle(envPath, envHandler)

	pgService := NewPreviewGroupService(c)
	pgPath, pgHandler := divergev1alpha1connect.NewPreviewGroupServiceHandler(pgService)
	mux.Handle(pgPath, pgHandler)

	clusterService := NewClusterService()
	clusterPath, clusterHandler := divergev1alpha1connect.NewClusterServiceHandler(clusterService)
	mux.Handle(clusterPath, clusterHandler)

	authService := NewAuthService()
	authPath, authHandler := divergev1alpha1connect.NewAuthServiceHandler(authService)
	mux.Handle(authPath, authHandler)

	return mux
}
