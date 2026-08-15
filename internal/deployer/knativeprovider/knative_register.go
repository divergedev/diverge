//go:build !no_knative

package knativeprovider

import (
	"github.com/divergedev/diverge/internal/deployer"
	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	deployer.Providers.Register("knative", registry.Provider[deployer.Deployer]{
		Create: func(deps registry.Deps) (deployer.Deployer, error) {
			return &KNativeDeployer{
				Client: deps.Client,
			}, nil
		},
		Description: "Knative Serving deployment",
	})
}
