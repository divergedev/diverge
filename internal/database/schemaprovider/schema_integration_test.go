//go:build integration

package schemaprovider

import (
	"context"
	"os"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSchemaIsolation_Integration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("Skipping DB schema isolation integration test because TEST_DATABASE_URL is not set")
	}

	provider := &SchemaDatabaseProvider{AdminDSN: dsn}
	ctx := context.Background()

	// 1. Create two environments with different names
	env1 := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "test-env-one"}}
	env2 := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "test-env-two"}}

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

	// 4. Deterministic naming convention
	assert.Equal(t, "preview_test_env_one", schema1)
	assert.Equal(t, "preview_test_env_two", schema2)

	// Verify schemas exist in DB
	status1, err := provider.Status(ctx, env1)
	require.NoError(t, err)
	assert.True(t, status1.Provisioned)

	status2, err := provider.Status(ctx, env2)
	require.NoError(t, err)
	assert.True(t, status2.Provisioned)

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

	// Cleanup env2
	err = provider.Teardown(ctx, env2)
	require.NoError(t, err)
}
