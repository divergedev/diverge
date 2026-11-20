package neon

import (
	"os"

	pkgdb "github.com/divergedev/diverge/pkg/database"
	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	pkgdb.Providers.Register("neon", registry.Provider[pkgdb.DatabaseProvider]{
		Create: func(deps registry.Deps) (pkgdb.DatabaseProvider, error) {
			return &NeonDatabaseProvider{
				Client:         deps.Client,
				APIKey:         os.Getenv("DIVERGE_NEON_API_KEY"),
				ProjectID:      os.Getenv("DIVERGE_NEON_PROJECT_ID"),
				ParentBranchID: os.Getenv("DIVERGE_NEON_PARENT_BRANCH_ID"),
				DatabaseName:   os.Getenv("DIVERGE_NEON_DATABASE_NAME"),
				RoleName:       os.Getenv("DIVERGE_NEON_ROLE_NAME"),
				BaseURL:        os.Getenv("DIVERGE_NEON_API_BASE_URL"),
			}, nil
		},
		Description: "Neon Serverless Postgres instant Copy-on-Write branch per environment",
	})
}
