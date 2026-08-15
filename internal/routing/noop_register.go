package routing

import "github.com/divergedev/diverge/pkg/registry"

func init() {
	Providers.Register("noop", registry.Provider[Router]{
		Create: func(deps registry.Deps) (Router, error) {
			return &NoopRouter{}, nil
		},
		Description: "No-op routing (disables routing features)",
	})
}
