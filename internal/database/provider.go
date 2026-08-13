package database

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// DatabaseProvider provisions and tears down database contexts for previews.
type DatabaseProvider interface {
	// Provision creates a database context for the preview environment.
	Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseResult, error)
	// Teardown removes the database context. Must be idempotent.
	Teardown(ctx context.Context, env *v1alpha1.Environment) error
	// Status returns the current state of the database context.
	Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error)
}

type DatabaseResult struct {
	DSN      string            // Connection string for the preview
	EnvVars  map[string]string // Env vars to inject into preview pod
	SetupSQL string            // SQL to run to initialize the database
	Ready    bool
	Message  string
}

type DatabaseStatus struct {
	Provisioned bool
	SchemaName  string
	Message     string
}
