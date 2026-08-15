package deployer

import "github.com/divergedev/diverge/pkg/registry"

func init() {
	Providers.Register("knative", registry.Provider[Deployer]{
		Create: func(deps registry.Deps) (Deployer, error) {
			return &KNativeDeployer{
				Client: deps.Client,
			}, nil
		},
		Description: "Knative Serving deployment",
	})
}
