package database

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/divergedev/diverge/api/v1alpha1"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type SchemaDatabaseProvider struct {
	AdminDSN string
}

func sanitizeEnvName(name string) string {
	s := strings.ReplaceAll(name, "-", "_")
	reg := regexp.MustCompile(`[^a-zA-Z0-9_]+`)
	s = reg.ReplaceAllString(s, "")
	if len(s) > 55 {
		s = s[:55]
	}
	return s
}

func (p *SchemaDatabaseProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseResult, error) {
	schema := fmt.Sprintf("preview_%s", sanitizeEnvName(env.Name))

	setupSQL := fmt.Sprintf(`DO $$
DECLARE
    row record;
BEGIN
    EXECUTE 'CREATE SCHEMA IF NOT EXISTS %s';
    FOR row IN
        SELECT table_name
        FROM information_schema.tables
        WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
    LOOP
        EXECUTE 'CREATE TABLE IF NOT EXISTS %s.' || quote_ident(row.table_name) || ' (LIKE public.' || quote_ident(row.table_name) || ' INCLUDING ALL)';
    END LOOP;
END
$$;`, schema, schema)

	dsn := p.AdminDSN
	if strings.Contains(dsn, "?") {
		dsn += "&search_path=" + schema + ",public"
	} else {
		dsn += "?search_path=" + schema + ",public"
	}

	return &DatabaseResult{
		DSN: dsn,
		EnvVars: map[string]string{
			"DATABASE_URL":           dsn,
			"DIVERGE_PREVIEW_SCHEMA": schema,
		},
		SetupSQL: setupSQL,
		Ready:    true,
		Message:  "Schema Provisioned SQL generated",
	}, nil
}

func (p *SchemaDatabaseProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	schema := fmt.Sprintf("preview_%s", sanitizeEnvName(env.Name))

	db, err := sql.Open("pgx", p.AdminDSN)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	query := fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)
	_, err = db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to drop schema: %w", err)
	}

	return nil
}

func (p *SchemaDatabaseProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	schema := fmt.Sprintf("preview_%s", sanitizeEnvName(env.Name))

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
