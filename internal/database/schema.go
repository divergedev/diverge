package database

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"time"
)

// SQLExecutor defines the contract for this component.
type SQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// SchemaDatabaseProvider represents the configuration or state for this type.
type SchemaDatabaseProvider struct {
	AdminDSN string
	Executor SQLExecutor
}

var validSchemaName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ErrInvalidSchemaName defines a package-level variable.
var ErrInvalidSchemaName = fmt.Errorf("invalid schema name: must match ^[a-z0-9][a-z0-9_-]*$")

func sanitizeEnvName(name string) (string, error) {
	s := strings.ToLower(strings.ReplaceAll(name, "-", "_"))
	if len(s) > 55 {
		s = s[:55]
	}
	if s == "" || !validSchemaName.MatchString(s) {
		return "", ErrInvalidSchemaName
	}
	return s, nil
}

// Provision performs its designated operation.
func (p *SchemaDatabaseProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseResult, error) {
	envName, err := sanitizeEnvName(env.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to sanitize environment name: %w", err)
	}
	schema := fmt.Sprintf("preview_%s", envName)

	setupSQL := fmt.Sprintf(`DO $$
DECLARE
    row record;
    seq_row record;
    col_row record;
BEGIN
    EXECUTE format('CREATE SCHEMA IF NOT EXISTS %%I', '%[1]s');
    FOR row IN
        SELECT table_name
        FROM information_schema.tables
        WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
    LOOP
        EXECUTE format('CREATE TABLE IF NOT EXISTS %%I.%%I (LIKE public.%%I INCLUDING ALL)', '%[1]s', row.table_name, row.table_name);
    END LOOP;

    -- For each sequence in public schema, create independent copy in preview schema
    FOR seq_row IN
        SELECT sequence_name FROM information_schema.sequences WHERE sequence_schema = 'public'
    LOOP
        EXECUTE format('CREATE SEQUENCE IF NOT EXISTS %%I.%%I', '%[1]s', seq_row.sequence_name);
    END LOOP;

    -- Re-map column defaults to use preview schema sequences
    FOR col_row IN
        SELECT table_name, column_name, column_default
        FROM information_schema.columns
        WHERE table_schema = '%[1]s'
        AND column_default LIKE 'nextval(''%%public.%%'
    LOOP
        EXECUTE format('ALTER TABLE %%I.%%I ALTER COLUMN %%I SET DEFAULT %%s',
            '%[1]s', col_row.table_name, col_row.column_name,
            replace(col_row.column_default, '''public.', '''%[1]s.'));
    END LOOP;
END
$$;`, schema)

	dsn := p.AdminDSN
	if strings.Contains(dsn, "?") {
		dsn += "&search_path=" + schema + ",public"
	} else {
		dsn += "?search_path=" + schema + ",public"
	}

	result := &DatabaseResult{
		DSN: dsn,
		EnvVars: map[string]string{
			"DATABASE_URL":           dsn,
			"DIVERGE_PREVIEW_SCHEMA": schema,
		},
		SetupSQL: fmt.Sprintf("SET LOCAL search_path TO %s, public;\n", schema) + setupSQL,
		Ready:    true,
		Message:  "Schema Provisioned SQL executed",
	}

	setupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var exec SQLExecutor
	if p.Executor != nil {
		exec = p.Executor
	} else {
		db, err := sql.Open("pgx", p.AdminDSN)
		if err != nil {
			result.Ready = false
			result.Message = fmt.Sprintf("failed to open database: %v", err)
			return result, nil
		}
		defer func() { _ = db.Close() }()
		exec = db
	}

	_, err = exec.ExecContext(setupCtx, setupSQL)
	if err != nil {
		result.Ready = false
		result.Message = fmt.Sprintf("failed to execute setup SQL: %v", err)
		return result, nil
	}

	return result, nil
}

// Teardown performs its designated operation.
func (p *SchemaDatabaseProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	envName, err := sanitizeEnvName(env.Name)
	if err != nil {
		return fmt.Errorf("failed to sanitize environment name: %w", err)
	}
	schema := fmt.Sprintf("preview_%s", envName)

	db, err := sql.Open("pgx", p.AdminDSN)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	schemaIdent := pgx.Identifier{schema}.Sanitize()
	query := fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaIdent)
	_, err = db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to drop schema: %w", err)
	}

	roleName := fmt.Sprintf("diverge_preview_%s", schema)
	roleIdent := pgx.Identifier{roleName}.Sanitize()
	query = fmt.Sprintf("DROP ROLE IF EXISTS %s", roleIdent)
	_, err = db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to drop role: %w", err)
	}

	return nil
}

// Status performs its designated operation.
func (p *SchemaDatabaseProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	envName, err := sanitizeEnvName(env.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to sanitize environment name: %w", err)
	}
	schema := fmt.Sprintf("preview_%s", envName)

	db, err := sql.Open("pgx", p.AdminDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	var exists bool
	err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)", schema).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("failed to check schema existence: %w", err)
	}

	if exists {
		return &DatabaseStatus{
			Provisioned: true,
			SchemaName:  schema,
			Message:     "Schema exists",
		}, nil
	}

	return &DatabaseStatus{
		Provisioned: false,
		SchemaName:  schema,
		Message:     "Schema does not exist",
	}, nil
}
