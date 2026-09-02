package routing

import "github.com/divergedev/diverge/pkg/registry"

func init() {
	Providers.Register("istio", registry.Provider[Router]{
		Create: func(deps registry.Deps) (Router, error) {
			return &IstioRouter{Client: deps.Client}, nil
		},
		Description: "Istio AuthorizationPolicy for intercept access control (no header routing)",
	})
}
