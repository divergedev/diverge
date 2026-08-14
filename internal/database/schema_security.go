package database

import (
	"context"
	"database/sql"
	"fmt"
)

func (p *SchemaDatabaseProvider) CreatePreviewRole(ctx context.Context, db *sql.DB, schemaName string) (string, error) {
	roleName := fmt.Sprintf("diverge_preview_%s", schemaName)

	query := fmt.Sprintf(`
		DO $$
		BEGIN
			IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = '%[2]s') THEN
				EXECUTE format('CREATE ROLE %%I LOGIN PASSWORD ''random''', '%[2]s');
			END IF;
			EXECUTE format('GRANT USAGE ON SCHEMA %%I TO %%I', '%[1]s', '%[2]s');
			EXECUTE format('GRANT ALL ON ALL TABLES IN SCHEMA %%I TO %%I', '%[1]s', '%[2]s');
			EXECUTE format('GRANT ALL ON ALL SEQUENCES IN SCHEMA %%I TO %%I', '%[1]s', '%[2]s');
			EXECUTE format('REVOKE ALL ON SCHEMA public FROM %%I', '%[2]s');
		END
		$$;
	`, schemaName, roleName)

	if _, err := db.ExecContext(ctx, query); err != nil {
		return "", fmt.Errorf("failed to create preview role: %w", err)
	}

	return roleName, nil
}
