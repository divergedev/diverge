package database

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgdb "github.com/divergedev/diverge/pkg/database"
)

// NoopDatabaseProvider represents the configuration or state for this type.
type NoopDatabaseProvider struct{}

var _ pkgdb.DatabaseProvider = (*NoopDatabaseProvider)(nil)

// Provision performs its designated operation.
func (p *NoopDatabaseProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*pkgdb.DatabaseResult, error) {
	return &pkgdb.DatabaseResult{
		Ready:   true,
		Message: "Noop provider always ready",
	}, nil
}

// Teardown performs its designated operation.
func (p *NoopDatabaseProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	return nil
}

// Status performs its designated operation.
func (p *NoopDatabaseProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*pkgdb.DatabaseStatus, error) {
	return &pkgdb.DatabaseStatus{
		Provisioned: true,
		Message:     "Noop provider always provisioned",
	}, nil
}
