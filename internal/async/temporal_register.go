//go:build !no_temporal

package async

import (
	"flag"
	"github.com/divergedev/diverge/pkg/registry"
)

var temporalNamespace string

func init() {
	flag.StringVar(&temporalNamespace, "temporal-namespace", "default", "Temporal namespace for async provisioning")

	Providers.Register("temporal", registry.Provider[Provisioner]{
		Create: func(deps registry.Deps) (Provisioner, error) {
			return &TemporalProvisioner{Namespace: temporalNamespace}, nil
		},
		Description: "Temporal task queue provisioner (lazy creation)",
	})
}
