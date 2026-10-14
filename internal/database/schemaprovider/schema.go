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
	"sync"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgdb "github.com/divergedev/diverge/pkg/database"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// durablePasswords stores generated role passwords keyed by adminDSN/namespace/roleName
// so passwords survive provider re-instantiation even when no Kubernetes Client is configured,
// and are cleared on Teardown.
var durablePasswords sync.Map

// SQLExecutor defines the contract for this component.
type SQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// SchemaDatabaseProvider represents the configuration or state for this type.
type SchemaDatabaseProvider struct {
	AdminDSN  string
	Executor  SQLExecutor
	Client    client.Client
	mu        sync.Mutex
	passwords map[string]string
}

// NewSchemaDatabaseProvider creates a SchemaDatabaseProvider with an initialized password cache.
func NewSchemaDatabaseProvider(adminDSN string) *SchemaDatabaseProvider {
	return &SchemaDatabaseProvider{
		AdminDSN:  adminDSN,
		passwords: make(map[string]string),
	}
}

func (p *SchemaDatabaseProvider) durableKey(namespace, roleName string) string {
	return p.AdminDSN + "/" + namespace + "/" + roleName
}

func rolePasswordSecretName(roleName string) string {
	s := strings.ToLower(strings.ReplaceAll(roleName, "_", "-"))
	const prefix = "diverge-db-role-"
	const maxLen = 63
	name := prefix + s
	if len(name) <= maxLen {
		return name
	}
	hash := sha256.Sum256([]byte(roleName))
	suffix := hex.EncodeToString(hash[:4])
	truncLen := maxLen - len(prefix) - 1 - 8
	return strings.TrimRight(prefix+s[:truncLen], "-") + "-" + suffix
}

func (p *SchemaDatabaseProvider) getOrCreatePassword(ctx context.Context, env *v1alpha1.Environment, roleName string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.passwords == nil {
		p.passwords = make(map[string]string)
	}
	if pw, ok := p.passwords[roleName]; ok && pw != "" {
		return pw, nil
	}

	ns := "default"
	if env != nil && env.Namespace != "" {
		ns = env.Namespace
	}
	dKey := p.durableKey(ns, roleName)
	secName := rolePasswordSecretName(roleName)

	// 1. Load from durable Kubernetes Secret if Client is configured
	if p.Client != nil {
		var sec corev1.Secret
		err := p.Client.Get(ctx, types.NamespacedName{Name: secName, Namespace: ns}, &sec)
		if err == nil {
			if pw := string(sec.Data["password"]); pw != "" {
				p.passwords[roleName] = pw
				durablePasswords.Store(dKey, pw)
				return pw, nil
			}
		} else if !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("failed to get role password secret %s/%s: %w", ns, secName, err)
		}
	}

	// 2. Fallback to durable store across provider instances
	if val, ok := durablePasswords.Load(dKey); ok {
		if pw, ok := val.(string); ok && pw != "" {
			p.passwords[roleName] = pw
			if p.Client != nil {
				if err := p.persistPasswordSecret(ctx, ns, secName, roleName, pw); err != nil {
					return "", err
				}
			}
			return pw, nil
		}
	}

	// 3. Generate a new password and persist it durably
	pwBytes := make([]byte, 32)
	if _, err := crypto_rand.Read(pwBytes); err != nil {
		return "", fmt.Errorf("failed to generate password: %w", err)
	}
	pw := hex.EncodeToString(pwBytes)

	if p.Client != nil {
		if err := p.persistPasswordSecret(ctx, ns, secName, roleName, pw); err != nil {
			return "", err
		}
	}

	p.passwords[roleName] = pw
	durablePasswords.Store(dKey, pw)
	return pw, nil
}

func (p *SchemaDatabaseProvider) persistPasswordSecret(ctx context.Context, namespace, secretName, roleName, password string) error {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				"divergedev.com/managed-by": "diverge",
				"divergedev.com/db-role":    roleName,
			},
		},
		Data: map[string][]byte{
			"password": []byte(password),
		},
	}
	if err := p.Client.Create(ctx, sec); err != nil {
		if apierrors.IsAlreadyExists(err) {
			var existing corev1.Secret
			if getErr := p.Client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, &existing); getErr != nil {
				return fmt.Errorf("failed to get existing role password secret %s/%s: %w", namespace, secretName, getErr)
			}
			if existing.Data == nil {
				existing.Data = make(map[string][]byte)
			}
			existing.Data["password"] = []byte(password)
			if updErr := p.Client.Update(ctx, &existing); updErr != nil {
				return fmt.Errorf("failed to update role password secret %s/%s: %w", namespace, secretName, updErr)
			}
			return nil
		}
		return fmt.Errorf("failed to create role password secret %s/%s: %w", namespace, secretName, err)
	}
	return nil
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

	roleName := safeRoleName(schema)
	password, err := p.getOrCreatePassword(ctx, env, roleName)
	if err != nil {
		return nil, err
	}

	schemaIdent := pgx.Identifier{schema}.Sanitize()
	roleIdent := pgx.Identifier{roleName}.Sanitize()

	// Idempotent role creation: create if not exists without rotating existing role password
	// editorconfig-checker-disable
	setupSQL += fmt.Sprintf(`
DO $diverge_role$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%s') THEN
    CREATE ROLE %s WITH LOGIN PASSWORD '%s';
  END IF;
END
$diverge_role$;
`, roleName, roleIdent, password)
	// editorconfig-checker-enable
	setupSQL += fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s;\n", schemaIdent, roleIdent)
	setupSQL += fmt.Sprintf("GRANT CREATE ON SCHEMA %s TO %s;\n", schemaIdent, roleIdent)
	setupSQL += fmt.Sprintf("GRANT ALL ON ALL TABLES IN SCHEMA %s TO %s;\n", schemaIdent, roleIdent)
	setupSQL += fmt.Sprintf("GRANT ALL ON ALL SEQUENCES IN SCHEMA %s TO %s;\n", schemaIdent, roleIdent)
	setupSQL += fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT ALL ON TABLES TO %s;\n", schemaIdent, roleIdent)
	setupSQL += fmt.Sprintf("REVOKE CREATE ON SCHEMA public FROM %s;\n", roleIdent)
	setupSQL += fmt.Sprintf("REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON ALL TABLES IN SCHEMA public FROM %s;\n", roleIdent)
	setupSQL += fmt.Sprintf("GRANT USAGE ON SCHEMA public TO %s;\n", roleIdent)
	setupSQL += fmt.Sprintf("GRANT SELECT ON ALL TABLES IN SCHEMA public TO %s;\n", roleIdent)
	// editorconfig-checker-disable
	setupSQL += fmt.Sprintf(`DO $diverge_verify_public$
DECLARE
    writable_table text;
BEGIN
    IF has_schema_privilege('%[1]s', 'public', 'CREATE') THEN
        RAISE EXCEPTION 'preview role %% still has effective CREATE privilege on schema public (inherited from PUBLIC or another role)', '%[1]s';
    END IF;
    SELECT format('%%I.%%I', schemaname, tablename) INTO writable_table
    FROM pg_tables
    WHERE schemaname = 'public'
      AND (
          has_table_privilege('%[1]s', format('%%I.%%I', schemaname, tablename), 'INSERT')
          OR has_table_privilege('%[1]s', format('%%I.%%I', schemaname, tablename), 'UPDATE')
          OR has_table_privilege('%[1]s', format('%%I.%%I', schemaname, tablename), 'DELETE')
          OR has_table_privilege('%[1]s', format('%%I.%%I', schemaname, tablename), 'TRUNCATE')
      )
    LIMIT 1;
    IF writable_table IS NOT NULL THEN
        RAISE EXCEPTION 'preview role %% still has effective write privilege on public table %% (inherited from PUBLIC or another role)', '%[1]s', writable_table;
    END IF;
END
$diverge_verify_public$;
`, roleName)
	// editorconfig-checker-enable

	seedSQL, err := p.buildSeedSQL(ctx, env, schema, schemaIdent)
	if err != nil {
		return nil, err
	}
	setupSQL += seedSQL

	dsn := buildWorkloadDSN(p.AdminDSN, roleName, password, schema)

	result := &pkgdb.DatabaseResult{
		DSN:      dsn,
		AdminDSN: p.AdminDSN,
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

	result.SetupSQLExecuted = true
	return result, nil
}

func (p *SchemaDatabaseProvider) buildSeedSQL(ctx context.Context, env *v1alpha1.Environment, schema, schemaIdent string) (string, error) {
	if env == nil {
		return "", nil
	}
	seedSource := strings.TrimSpace(env.Spec.Database.SeedSource)
	if seedSource == "" {
		return "", nil
	}

	if seedSource == "public" || strings.HasPrefix(seedSource, "schema:") {
		srcSchema := "public"
		if strings.HasPrefix(seedSource, "schema:") {
			srcSchema = strings.TrimSpace(strings.TrimPrefix(seedSource, "schema:"))
		}
		if srcSchema == "" || !validSchemaName.MatchString(srcSchema) {
			return "", fmt.Errorf("invalid seedSource schema %q: %w", srcSchema, ErrInvalidSchemaName)
		}
		// editorconfig-checker-disable
		return fmt.Sprintf(`
DO $diverge_seed$
DECLARE
    row record;
    col_list text;
    incompatible_col text;
    seq_rec record;
    seq_name text;
BEGIN
    FOR row IN
        SELECT p_tbl.table_name
        FROM information_schema.tables p_tbl
        JOIN information_schema.tables s_tbl
          ON s_tbl.table_name = p_tbl.table_name
        WHERE p_tbl.table_schema = '%[1]s' AND p_tbl.table_type = 'BASE TABLE'
          AND s_tbl.table_schema = '%[2]s' AND s_tbl.table_type = 'BASE TABLE'
    LOOP
        SELECT p_col.column_name INTO incompatible_col
        FROM information_schema.columns p_col
        LEFT JOIN information_schema.columns s_col
          ON s_col.table_schema = '%[2]s'
         AND s_col.table_name = row.table_name
         AND s_col.column_name = p_col.column_name
        WHERE p_col.table_schema = '%[1]s'
          AND p_col.table_name = row.table_name
          AND COALESCE(p_col.is_generated, 'NEVER') = 'NEVER'
          AND (
              (s_col.column_name IS NOT NULL AND p_col.udt_name <> s_col.udt_name)
              OR (s_col.column_name IS NULL AND p_col.is_nullable = 'NO' AND p_col.column_default IS NULL AND COALESCE(p_col.is_identity, 'NO') = 'NO')
          )
        LIMIT 1;

        IF incompatible_col IS NOT NULL THEN
            RAISE EXCEPTION 'incompatible column layout for column %% between preview table %%I.%%I and source table %%I.%%I',
                incompatible_col, '%[1]s', row.table_name, '%[2]s', row.table_name;
        END IF;

        SELECT string_agg(quote_ident(p_col.column_name), ', ' ORDER BY p_col.ordinal_position) INTO col_list
        FROM information_schema.columns p_col
        JOIN information_schema.columns s_col
          ON s_col.table_schema = '%[2]s'
         AND s_col.table_name = row.table_name
         AND s_col.column_name = p_col.column_name
        WHERE p_col.table_schema = '%[1]s'
          AND p_col.table_name = row.table_name
          AND COALESCE(p_col.is_generated, 'NEVER') = 'NEVER'
          AND p_col.udt_name = s_col.udt_name;

        IF col_list IS NOT NULL AND col_list <> '' THEN
            EXECUTE format('INSERT INTO %%I.%%I (%%s) OVERRIDING SYSTEM VALUE SELECT %%s FROM %%I.%%I ON CONFLICT DO NOTHING',
                '%[1]s', row.table_name, col_list, col_list, '%[2]s', row.table_name);
        END IF;

        FOR seq_rec IN
            SELECT column_name
            FROM information_schema.columns
            WHERE table_schema = '%[1]s'
              AND table_name = row.table_name
        LOOP
            seq_name := pg_get_serial_sequence(format('%%I.%%I', '%[1]s', row.table_name), seq_rec.column_name);
            IF seq_name IS NOT NULL THEN
                EXECUTE format(
                    'SELECT setval(%%L, GREATEST(COALESCE((SELECT MAX(%%I)::bigint FROM %%I.%%I), 1), 1), (SELECT COUNT(%%I) > 0 FROM %%I.%%I))',
                    seq_name, seq_rec.column_name, '%[1]s', row.table_name, seq_rec.column_name, '%[1]s', row.table_name
                );
            END IF;
        END LOOP;
    END LOOP;
END
$diverge_seed$;
`, schema, srcSchema), nil
		// editorconfig-checker-enable
	}

	cmSpec := strings.TrimSpace(strings.TrimPrefix(seedSource, "configmap:"))
	cmName := cmSpec
	key := "seed.sql"
	if idx := strings.IndexByte(cmSpec, '/'); idx >= 0 {
		cmName = strings.TrimSpace(cmSpec[:idx])
		if k := strings.TrimSpace(cmSpec[idx+1:]); k != "" {
			key = k
		}
	}
	if cmName == "" {
		return "", fmt.Errorf("invalid seedSource %q: configmap name cannot be empty", seedSource)
	}
	if p.Client == nil {
		return "", fmt.Errorf("kubernetes client is required to load seedSource ConfigMap %q", cmSpec)
	}

	ns := "default"
	if env.Namespace != "" {
		ns = env.Namespace
	}
	var cm corev1.ConfigMap
	if err := p.Client.Get(ctx, types.NamespacedName{Name: cmName, Namespace: ns}, &cm); err != nil {
		return "", fmt.Errorf("failed to get seedSource ConfigMap %s/%s: %w", ns, cmName, err)
	}

	sqlText, ok := cm.Data[key]
	if !ok && key == "seed.sql" {
		var sqlKeys []string
		for k := range cm.Data {
			if strings.HasSuffix(k, ".sql") {
				sqlKeys = append(sqlKeys, k)
			}
		}
		if len(sqlKeys) == 1 {
			sqlText = cm.Data[sqlKeys[0]]
			ok = true
		}
	}
	if !ok || strings.TrimSpace(sqlText) == "" {
		return "", fmt.Errorf("seedSource ConfigMap %s/%s does not contain non-empty SQL in key %q", ns, cmName, key)
	}

	return fmt.Sprintf("\n-- Seed SQL from ConfigMap %s\nSET LOCAL search_path TO %s, public;\n%s\n", cm.Name, schemaIdent, strings.TrimSpace(sqlText)), nil
}

// Teardown performs its designated operation.
func (p *SchemaDatabaseProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	envName, err := sanitizeEnvName(env.Name)
	if err != nil {
		return fmt.Errorf("failed to sanitize environment name: %w", err)
	}
	schema := fmt.Sprintf("preview_%s", envName)

	var exec SQLExecutor
	if p.Executor != nil {
		exec = p.Executor
	} else {
		db, err := sql.Open("pgx", p.AdminDSN)
		if err != nil {
			return fmt.Errorf("failed to open database: %w", err)
		}
		defer func() { _ = db.Close() }()
		exec = db
	}

	schemaIdent := pgx.Identifier{schema}.Sanitize()
	query := fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaIdent)
	_, err = exec.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to drop schema: %w", err)
	}

	roleName := safeRoleName(schema)
	roleIdent := pgx.Identifier{roleName}.Sanitize()
	query = fmt.Sprintf("DROP ROLE IF EXISTS %s", roleIdent)
	_, err = exec.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to drop role: %w", err)
	}

	ns := "default"
	if env != nil && env.Namespace != "" {
		ns = env.Namespace
	}
	if p.Client != nil {
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rolePasswordSecretName(roleName),
				Namespace: ns,
			},
		}
		if delErr := client.IgnoreNotFound(p.Client.Delete(ctx, sec)); delErr != nil {
			return fmt.Errorf("failed to delete role password secret: %w", delErr)
		}
	}

	p.mu.Lock()
	delete(p.passwords, roleName)
	durablePasswords.Delete(p.durableKey(ns, roleName))
	p.mu.Unlock()

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
