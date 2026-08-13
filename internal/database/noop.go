package database

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type NoopDatabaseProvider struct{}

func (p *NoopDatabaseProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseResult, error) {
	return &DatabaseResult{
		Ready:   true,
		Message: "Noop provider always ready",
	}, nil
}

func (p *NoopDatabaseProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	return nil
}

func (p *NoopDatabaseProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{
		Provisioned: true,
		Message:     "Noop provider always provisioned",
	}, nil
}
