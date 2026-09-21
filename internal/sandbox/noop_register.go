package sandbox

import (
	"github.com/divergedev/diverge/pkg/registry"
	pkgsandbox "github.com/divergedev/diverge/pkg/sandbox"
)

func init() {
	pkgsandbox.Providers.Register("noop", registry.Provider[pkgsandbox.SandboxProvider]{
		Create: func(deps registry.Deps) (pkgsandbox.SandboxProvider, error) {
			return &NoopSandboxProvider{}, nil
		},
		Description: "No-op sandbox provider for testing",
	})
	pkgsandbox.Providers.Register("none", registry.Provider[pkgsandbox.SandboxProvider]{
		Create: func(deps registry.Deps) (pkgsandbox.SandboxProvider, error) {
			return &NoopSandboxProvider{}, nil
		},
		Description: "Alias for no-op sandbox provider",
	})
}
