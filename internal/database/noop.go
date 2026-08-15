package database

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// NoopDatabaseProvider represents the configuration or state for this type.
type NoopDatabaseProvider struct{}

var _ DatabaseProvider = (*NoopDatabaseProvider)(nil)

// Provision performs its designated operation.
func (p *NoopDatabaseProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseResult, error) {
	return &DatabaseResult{
		Ready:   true,
		Message: "Noop provider always ready",
	}, nil
}

// Teardown performs its designated operation.
func (p *NoopDatabaseProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	return nil
}

// Status performs its designated operation.
func (p *NoopDatabaseProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{
		Provisioned: true,
		Message:     "Noop provider always provisioned",
	}, nil
}
