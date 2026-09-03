// Package database defines the public API for Diverge database providers.
//
// This package contains the interfaces and types that third-party and
// Pro/Enterprise providers implement to integrate with the Diverge controller.
// OSS implementations live in internal/database; Pro implementations live in
// the diverge-enterprise repository.
//
// # Implementing a custom provider
//
// To implement a custom DatabaseProvider, implement the [DatabaseProvider]
// interface and register it using [RegisterProvider].
//
//	type MyProvider struct { /* config */ }
//
//	func (p *MyProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*database.DatabaseResult, error) {
//	    // Create database context for the preview
//	}
//
//	func (p *MyProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
//	    // Remove database context
//	}
//
//	func (p *MyProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*database.DatabaseStatus, error) {
//	    // Report current state
//	}
package database

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// DatabaseProvider provisions and tears down database contexts for preview
// environments. Implementations range from simple (noop, schema isolation) to
// complex (Neon branching, CloudNativePG instances).
//
// All methods must be safe for concurrent use.
type DatabaseProvider interface {
	// Provision creates a database context for the preview environment.
	// It returns a DatabaseResult with connection details and any env vars
	// to inject into preview pods.
	Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseResult, error)

	// Teardown removes the database context. Must be idempotent — calling
	// Teardown on an already-torn-down environment must not return an error.
	Teardown(ctx context.Context, env *v1alpha1.Environment) error

	// Status returns the current state of the database context.
	Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error)
}

// DatabaseResult is the outcome of a Provision call.
type DatabaseResult struct {
	// DSN is the connection string for the preview database.
	DSN string

	// EnvVars are environment variables to inject into preview pods.
	// Typically includes DATABASE_URL and schema-specific vars.
	EnvVars map[string]string

	// SetupSQL is SQL to run to initialize the database context.
	// The controller executes this against the admin DSN after Provision
	// returns. May be empty if the provider handles setup internally.
	SetupSQL string

	// AdminDSN is the connection string for the admin role, used to execute SetupSQL.
	AdminDSN string

	// Ready reports that the provider finished its own work. It does not
	// guarantee that SetupSQL has been executed; callers must run SetupSQL
	// separately if non-empty.
	Ready bool

	// Message is a human-readable status message for logging and MR comments.
	Message string
}

// DatabaseStatus is the observed state of a preview database context.
type DatabaseStatus struct {
	// Provisioned indicates whether the database context exists.
	Provisioned bool

	// SchemaName is the name of the database schema (for schema-based isolation).
	SchemaName string

	// Message is a human-readable status message.
	Message string
}
