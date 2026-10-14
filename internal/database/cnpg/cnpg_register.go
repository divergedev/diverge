package cnpg

import (
	"os"

	pkgdb "github.com/divergedev/diverge/pkg/database"
	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	createFn := func(deps registry.Deps) (pkgdb.DatabaseProvider, error) {
		storageSize := os.Getenv("DIVERGE_CNPG_STORAGE_SIZE")
		if storageSize == "" {
			storageSize = DefaultStorageSize
		}
		return &CNPGDatabaseProvider{
			Client:          deps.Client,
			Scheme:          deps.Scheme,
			StorageSize:     storageSize,
			Instances:       DefaultInstances,
			SourceCluster:   os.Getenv("DIVERGE_CNPG_SOURCE_CLUSTER"),
			SourceNamespace: os.Getenv("DIVERGE_CNPG_SOURCE_NAMESPACE"),
		}, nil
	}

	pkgdb.Providers.Register("cnpg", registry.Provider[pkgdb.DatabaseProvider]{
		Create:      createFn,
		Description: "CloudNativePG ephemeral Postgres cluster per environment",
	})
	pkgdb.Providers.Register("fresh", registry.Provider[pkgdb.DatabaseProvider]{
		Create:      createFn,
		Description: "Alias for CloudNativePG fresh cluster per environment",
	})
	pkgdb.Providers.Register("snapshot", registry.Provider[pkgdb.DatabaseProvider]{
		Create:      createFn,
		Description: "Alias for CloudNativePG snapshot/clone cluster per environment",
	})
}
