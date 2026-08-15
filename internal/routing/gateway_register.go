package routing

import "github.com/divergedev/diverge/pkg/registry"

func init() {
	Providers.Register("gateway", registry.Provider[Router]{
		Create: func(deps registry.Deps) (Router, error) {
			return &GatewayRouter{Client: deps.Client}, nil
		},
		Description: "Gateway API HTTPRoute routing",
	})
}
