//go:build integration

package schemaprovider

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSchemaIsolation_Integration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("Skipping DB schema isolation integration test because TEST_DATABASE_URL is not set")
	}

	// P3 fix: context timeout to prevent CI hangs
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	provider := &SchemaDatabaseProvider{AdminDSN: dsn}

	// 1. Create two environments with different names
	env1 := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "test-env-one"}}
	env2 := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "test-env-two"}}

	// P0 fix: always clean up schemas, even on test failure
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = provider.Teardown(cleanupCtx, env1)
		_ = provider.Teardown(cleanupCtx, env2)
	})

	// Provision env1
	res1, err := provider.Provision(ctx, env1)
	require.NoError(t, err)
	require.True(t, res1.Ready, "Environment 1 database should be ready")
	schema1 := res1.EnvVars["DIVERGE_PREVIEW_SCHEMA"]
	require.NotEmpty(t, schema1, "Schema 1 should not be empty")

	// Provision env2
	res2, err := provider.Provision(ctx, env2)
	require.NoError(t, err)
	require.True(t, res2.Ready, "Environment 2 database should be ready")
	schema2 := res2.EnvVars["DIVERGE_PREVIEW_SCHEMA"]
	require.NotEmpty(t, schema2, "Schema 2 should not be empty")

	// 2. Distinct schemas/namespaces
	assert.NotEqual(t, schema1, schema2, "Schemas should be isolated per-environment (distinct schemas)")

	// P2 fix: verify naming convention without hardcoding exact names
	assert.True(t, strings.HasPrefix(schema1, "preview_"), "Schema 1 should have preview_ prefix, got: %s", schema1)
	assert.True(t, strings.HasPrefix(schema2, "preview_"), "Schema 2 should have preview_ prefix, got: %s", schema2)
	// Verify determinism via sanitizeEnvName directly
	expected1, err := sanitizeEnvName(env1.Name)
	require.NoError(t, err)
	assert.Equal(t, "preview_"+expected1, schema1, "Schema name should be deterministic")

	// Verify schemas exist in DB
	status1, err := provider.Status(ctx, env1)
	require.NoError(t, err)
	assert.True(t, status1.Provisioned)

	status2, err := provider.Status(ctx, env2)
	require.NoError(t, err)
	assert.True(t, status2.Provisioned)

	// P1 fix: verify DATA isolation, not just schema existence
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Write a probe table + row into schema1
	_, err = db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+schema1+".isolation_probe (id int)")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "INSERT INTO "+schema1+".isolation_probe VALUES (42)")
	require.NoError(t, err)

	// Verify the probe table does NOT exist in schema2
	var count int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+schema2+".isolation_probe").Scan(&count)
	assert.Error(t, err, "Data from env1 should not be visible in env2's schema (table should not exist)")

	// 3. Deleting an Environment cleans up its schema
	err = provider.Teardown(ctx, env1)
	require.NoError(t, err)

	status1After, err := provider.Status(ctx, env1)
	require.NoError(t, err)
	assert.False(t, status1After.Provisioned, "Schema 1 should be deleted after teardown")

	// Verify env2 schema still exists
	status2After, err := provider.Status(ctx, env2)
	require.NoError(t, err)
	assert.True(t, status2After.Provisioned, "Schema 2 should still exist after env1 teardown")
}
