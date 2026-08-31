//go:build !no_schema

package schemaprovider

import (
	"context"
	crypto_rand "crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgdb "github.com/divergedev/diverge/pkg/database"
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
func (p *SchemaDatabaseProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*pkgdb.DatabaseResult, error) {
	envName, err := sanitizeEnvName(env.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to sanitize environment name: %w", err)
	}
	schema := fmt.Sprintf("preview_%s", envName)

	// editorconfig-checker-disable
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
	// editorconfig-checker-enable

	pwBytes := make([]byte, 32)
	if _, err := crypto_rand.Read(pwBytes); err != nil {
		return nil, fmt.Errorf("failed to generate password: %w", err)
	}
	password := hex.EncodeToString(pwBytes)
	roleName := safeRoleName(schema)

	schemaIdent := pgx.Identifier{schema}.Sanitize()
	roleIdent := pgx.Identifier{roleName}.Sanitize()

	// Idempotent role creation: create if not exists, update password on re-reconcile
	// editorconfig-checker-disable
	setupSQL += fmt.Sprintf(`
DO $diverge_role$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%s') THEN
    CREATE ROLE %s WITH LOGIN PASSWORD '%s';
  ELSE
    ALTER ROLE %s WITH PASSWORD '%s';
  END IF;
END
$diverge_role$;
`, roleName, roleIdent, password, roleIdent, password)
	// editorconfig-checker-enable
	setupSQL += fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s;\n", schemaIdent, roleIdent)
	setupSQL += fmt.Sprintf("GRANT ALL ON ALL TABLES IN SCHEMA %s TO %s;\n", schemaIdent, roleIdent)
	setupSQL += fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT ALL ON TABLES TO %s;\n", schemaIdent, roleIdent)

	dsn := buildWorkloadDSN(p.AdminDSN, roleName, password, schema)

	result := &pkgdb.DatabaseResult{
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

	roleName := safeRoleName(schema)
	roleIdent := pgx.Identifier{roleName}.Sanitize()
	query = fmt.Sprintf("DROP ROLE IF EXISTS %s", roleIdent)
	_, err = db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to drop role: %w", err)
	}

	return nil
}

// Status performs its designated operation.
func (p *SchemaDatabaseProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*pkgdb.DatabaseStatus, error) {
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
		return &pkgdb.DatabaseStatus{
			Provisioned: true,
			SchemaName:  schema,
			Message:     "Schema exists",
		}, nil
	}

	return &pkgdb.DatabaseStatus{
		Provisioned: false,
		SchemaName:  schema,
		Message:     "Schema does not exist",
	}, nil
}

// safeRoleName generates a PostgreSQL role name that fits within the
// 63-byte NAMEDATALEN limit. For short schemas the name is simply
// "diverge_preview_<schema>". For long schemas the name is truncated
// and a stable 8-char hash suffix is appended for uniqueness.
func safeRoleName(schema string) string {
	const prefix = "diverge_preview_"
	const maxLen = 63 // PostgreSQL NAMEDATALEN - 1

	name := prefix + schema
	if len(name) <= maxLen {
		return name
	}

	// Truncate and append stable hash to avoid collisions
	hash := sha256.Sum256([]byte(schema))
	suffix := hex.EncodeToString(hash[:4]) // 8 hex chars
	// prefix(16) + truncated + _ + suffix(8) = maxLen
	truncLen := maxLen - len(prefix) - 1 - 8
	return prefix + schema[:truncLen] + "_" + suffix
}

// buildWorkloadDSN constructs a connection string for the per-schema
// restricted role. It supports both URL-form (postgres://user@host/db)
// and pgx keyword/value-form (user=admin host=localhost dbname=db).
func buildWorkloadDSN(adminDSN, roleName, password, schema string) string {
	if isURLFormDSN(adminDSN) {
		parsed, err := url.Parse(adminDSN)
		if err != nil {
			// Fallback: build a simple DSN
			return fmt.Sprintf("postgres://%s:%s@localhost/%s?search_path=%s,public",
				roleName, password, schema, schema)
		}
		parsed.User = url.UserPassword(roleName, password)
		dsn := parsed.String()
		if strings.Contains(dsn, "?") {
			dsn += "&search_path=" + schema + ",public"
		} else {
			dsn += "?search_path=" + schema + ",public"
		}
		return dsn
	}

	// keyword/value format: replace or add user/password, append search_path
	parts := parseKeyValueDSN(adminDSN)
	parts["user"] = roleName
	parts["password"] = password
	parts["search_path"] = schema + ",public"
	var sb strings.Builder
	for k, v := range parts {
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		// Quote values containing spaces
		if strings.ContainsAny(v, " '\\") {
			sb.WriteString("'" + strings.ReplaceAll(v, "'", "\\'") + "'")
		} else {
			sb.WriteString(v)
		}
	}
	return sb.String()
}

// isURLFormDSN returns true if the DSN looks like a postgres:// or postgresql:// URL.
func isURLFormDSN(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
}

// parseKeyValueDSN parses a pgx keyword/value connection string into a map.
func parseKeyValueDSN(dsn string) map[string]string {
	result := make(map[string]string)
	for _, part := range strings.Fields(dsn) {
		if idx := strings.IndexByte(part, '='); idx > 0 {
			key := part[:idx]
			val := strings.Trim(part[idx+1:], "'")
			result[key] = val
		}
	}
	return result
}
