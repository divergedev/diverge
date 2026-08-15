package testing

import "github.com/divergedev/diverge/pkg/registry"

func init() {
	Providers.Register("noop", registry.Provider[TestRunner]{
		Create: func(deps registry.Deps) (TestRunner, error) {
			return &NoopTestRunner{}, nil
		},
		Description: "No-op test runner",
	})
}
