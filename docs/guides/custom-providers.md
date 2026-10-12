# Write a Custom Provider in 50 Lines

Diverge uses a generic, thread-safe provider registry (`pkg/registry`) for all 6 extension points:
- **Routing Providers** (`pkg/routing.Providers`)
- **Database Providers** (`pkg/database.Providers`)
- **Deployer Providers** (`pkg/deployer.Providers`)
- **Async Provisioners** (`internal/async.Providers`)
- **SCM Notifiers** (`pkg/notifier.Providers`)
- **Sandbox Providers** (`pkg/sandbox.Providers`)

You can add a new provider in a single Go file without modifying the controller.

---

## Example: Custom Database Provider (`internal/database/myprovider.go`)

```go
package database

import (
	"context"
	"fmt"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgdb "github.com/divergedev/diverge/pkg/database"
	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	pkgdb.Providers.Register("my-cloud-db", func(deps registry.Deps) (pkgdb.DatabaseProvider, error) {
		return &MyCloudDBProvider{}, nil
	})
}

type MyCloudDBProvider struct{}

func (p *MyCloudDBProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*pkgdb.DatabaseResult, error) {
	dbName := fmt.Sprintf("preview_%s", env.Name)
	dsn := fmt.Sprintf("postgres://user:pass@my-cloud-db.internal:5432/%s?sslmode=require", dbName)
	return &pkgdb.DatabaseResult{
		DSN:        dsn,
		SchemaName: dbName,
		Ready:      true,
	}, nil
}

func (p *MyCloudDBProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	// Drop branch/database for env.Name
	return nil
}
```

---

## Using Your Provider

Pass `--db-provider=my-cloud-db` to `diverge-controller` or set `database.provider: my-cloud-db` in `charts/diverge/values.yaml`.
