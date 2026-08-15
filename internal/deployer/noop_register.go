package deployer

import "github.com/divergedev/diverge/pkg/registry"

func init() {
	Providers.Register("noop", registry.Provider[Deployer]{
		Create: func(deps registry.Deps) (Deployer, error) {
			return &NoopDeployer{}, nil
		},
		Description: "No-op deployment",
	})
}
