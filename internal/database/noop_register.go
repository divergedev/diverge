package database

import (
	pkgdb "github.com/divergedev/diverge/pkg/database"
	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	pkgdb.Providers.Register("noop", registry.Provider[pkgdb.DatabaseProvider]{
		Create: func(deps registry.Deps) (pkgdb.DatabaseProvider, error) {
			return &NoopDatabaseProvider{}, nil
		},
		Description: "No-op database provisioning",
	})
	pkgdb.Providers.Register("none", registry.Provider[pkgdb.DatabaseProvider]{
		Create: func(deps registry.Deps) (pkgdb.DatabaseProvider, error) {
			return &NoopDatabaseProvider{}, nil
		},
		Description: "Alias for noop database provisioning",
	})
}
