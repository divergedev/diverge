package routing

import "github.com/divergedev/diverge/pkg/registry"

func init() {
	Providers.Register("composite", registry.Provider[Router]{
		Create: func(deps registry.Deps) (Router, error) {
			return &CompositeRouter{
				Routers: map[string]Router{
					"gateway": &GatewayRouter{Client: deps.Client},
					"async": &AsyncRouter{
						Providers: []AsyncProvider{},
					},
				},
			}, nil
		},
		Description: "Composite routing with Gateway and Async components",
	})
}
