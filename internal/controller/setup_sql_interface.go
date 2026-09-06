package controller

import (
	"context"
	"database/sql"
	"fmt"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// SQLExecutor defines the contract for executing SQL queries.
type SQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// SetupRunner executes initial DDL or schema setup for provisioned databases.
type SetupRunner interface {
	RunSetupSQL(ctx context.Context, env *divergeiov1alpha1.Environment, setupSQL, adminDSN string) error
}

// DirectSetupRunner executes setup SQL in-process if it was not already handled by the database provider.
// This is the default runner for Diverge.
type DirectSetupRunner struct {
	// Executor is an optional SQLExecutor (e.g. *sql.DB). If nil, RunSetupSQL connects using adminDSN.
	Executor SQLExecutor
}

// RunSetupSQL executes setup SQL in-process against the admin DSN or using the configured executor.
func (r *DirectSetupRunner) RunSetupSQL(ctx context.Context, env *divergeiov1alpha1.Environment, setupSQL, adminDSN string) error {
	if setupSQL == "" {
		return nil
	}

	if r.Executor != nil {
		if _, err := r.Executor.ExecContext(ctx, setupSQL); err != nil {
			return fmt.Errorf("failed to execute setup SQL via executor: %w", err)
		}
		return nil
	}

	if adminDSN == "" {
		return fmt.Errorf("cannot execute setup SQL: admin DSN is empty and no executor provided")
	}

	db, err := sql.Open("pgx", adminDSN)
	if err != nil {
		return fmt.Errorf("failed to open database connection for setup SQL: %w", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, setupSQL); err != nil {
		return fmt.Errorf("failed to execute setup SQL: %w", err)
	}

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
