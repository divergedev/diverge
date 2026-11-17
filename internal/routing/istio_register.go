package routing

import (
	"github.com/divergedev/diverge/internal/multicluster"
	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	Providers.Register("istio", registry.Provider[Router]{
		Create: func(deps registry.Deps) (Router, error) {
			return &IstioRouter{
				Client:         deps.Client,
				ClusterClients: multicluster.NewManager(deps.Client, deps.Clientset, deps.Config, deps.Scheme),
			}, nil
		},
		Description: "Istio AuthorizationPolicy for intercept access control (no header routing)",
	})
}
