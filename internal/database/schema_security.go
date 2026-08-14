package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (p *SchemaDatabaseProvider) CreatePreviewRole(ctx context.Context, db *sql.DB, schemaName string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	roleName := fmt.Sprintf("diverge_preview_%s", schemaName)
	schemaIdent := pgx.Identifier{schemaName}.Sanitize()
	roleIdent := pgx.Identifier{roleName}.Sanitize()

	var exists bool
	err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT FROM pg_catalog.pg_roles WHERE rolname = $1)", roleName).Scan(&exists)
	if err != nil {
		return "", fmt.Errorf("failed to check role existence: %w", err)
	}

	if !exists {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE ROLE %s NOLOGIN", roleIdent)); err != nil {
			return "", fmt.Errorf("failed to create preview role: %w", err)
		}
	}

	queries := []string{
		fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", schemaIdent, roleIdent),
		fmt.Sprintf("GRANT ALL ON ALL TABLES IN SCHEMA %s TO %s", schemaIdent, roleIdent),
		fmt.Sprintf("GRANT ALL ON ALL SEQUENCES IN SCHEMA %s TO %s", schemaIdent, roleIdent),
		fmt.Sprintf("REVOKE USAGE ON SCHEMA public FROM %s", roleIdent),
	}

	for _, q := range queries {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return "", fmt.Errorf("failed to execute grant: %w", err)
		}
	}

	return roleName, nil
}
