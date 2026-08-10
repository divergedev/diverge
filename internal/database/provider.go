package database

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// DatabaseProvider defines the interface for database provisioning
type DatabaseProvider interface {
	Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error)
	Teardown(ctx context.Context, env *v1alpha1.Environment) error
	Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error)
}

type DatabaseStatus struct {
	Ready            bool
	ConnectionSecret string
	Message          string
}

// SharedProvider provides a shared database connection
type SharedProvider struct{}

func (p *SharedProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{}, nil
}
func (p *SharedProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error { return nil }
func (p *SharedProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{}, nil
}

// SchemaProvider creates a new logical schema within an existing database
type SchemaProvider struct{}

func (p *SchemaProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{}, nil
}
func (p *SchemaProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error { return nil }
func (p *SchemaProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{}, nil
}

// FreshProvider provisions a completely new database instance
type FreshProvider struct{}

func (p *FreshProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{}, nil
}
func (p *FreshProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error { return nil }
func (p *FreshProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{}, nil
}

// SnapshotProvider provisions a database from a snapshot
type SnapshotProvider struct{}

func (p *SnapshotProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{}, nil
}
func (p *SnapshotProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error { return nil }
func (p *SnapshotProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{}, nil
}

// NoopProvider is a dummy provider that does nothing
type NoopProvider struct{}

func (p *NoopProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{Ready: true, Message: "Noop provider"}, nil
}
func (p *NoopProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error { return nil }
func (p *NoopProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{Ready: true, Message: "Noop provider"}, nil
}
