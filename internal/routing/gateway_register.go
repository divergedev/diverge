package routing

import (
	"github.com/divergedev/diverge/internal/multicluster"
	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	Providers.Register("gateway", registry.Provider[Router]{
		Create: func(deps registry.Deps) (Router, error) {
			return &GatewayRouter{
				Client:         deps.Client,
				ClusterClients: multicluster.NewManager(deps.Client, deps.Clientset, deps.Config, deps.Scheme),
			}, nil
		},
		Description: "Gateway API HTTPRoute routing",
	})
}
