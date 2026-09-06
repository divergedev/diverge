package controller

import (
	"context"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

// SetupRunner executes initial DDL or schema setup for provisioned databases.
type SetupRunner interface {
	RunSetupSQL(ctx context.Context, env *divergeiov1alpha1.Environment, setupSQL, adminDSN string) error
}

// DirectSetupRunner executes setup SQL in-process if it was not already handled by the database provider.
// This is the default runner for Diverge.
type DirectSetupRunner struct{}

// RunSetupSQL is a no-op when the database provider already executed the setup SQL in-process.
func (r *DirectSetupRunner) RunSetupSQL(ctx context.Context, env *divergeiov1alpha1.Environment, setupSQL, adminDSN string) error {
	return nil
}

// JobSetupRunner executes setup SQL via a Kubernetes Job for isolated environments.
// Deprecated: In-process database setup via SQLExecutor is preferred for performance, reliability, and security.
type JobSetupRunner struct {
	Reconciler *EnvironmentReconciler
}

// RunSetupSQL delegates execution to the reconciler's runSetupSQLJob method.
func (r *JobSetupRunner) RunSetupSQL(ctx context.Context, env *divergeiov1alpha1.Environment, setupSQL, adminDSN string) error {
	return r.Reconciler.runSetupSQLJob(ctx, env, setupSQL, adminDSN)
}
