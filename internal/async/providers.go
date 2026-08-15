package async

import "github.com/divergedev/diverge/pkg/registry"

// Providers is the registry for async provisioners.
var Providers = registry.New[Provisioner]("async-provisioner")

func init() {
	Providers.Register("noop", registry.Provider[Provisioner]{
		Create: func(deps registry.Deps) (Provisioner, error) {
			return &NoopProvisioner{}, nil
		},
		Description: "No-op async provisioner (returns target with env name suffix)",
	})
}
