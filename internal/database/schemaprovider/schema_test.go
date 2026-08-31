//go:build !no_schema

package schemaprovider

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSchemaDatabaseProvider_Sanitize(t *testing.T) {
	name, err := sanitizeEnvName("my-test-env-123")
	assert.NoError(t, err)
	assert.Equal(t, "my_test_env_123", name)
}

func TestSchemaDatabaseProvider_Sanitize_RejectsSQLInjection(t *testing.T) {
	_, err := sanitizeEnvName("env; DROP TABLE users;")
	assert.ErrorIs(t, err, ErrInvalidSchemaName)

	_, err = sanitizeEnvName("env' OR 1=1--")
	assert.ErrorIs(t, err, ErrInvalidSchemaName)
}

func TestSchemaDatabaseProvider_Provision(t *testing.T) {
	provider := &SchemaDatabaseProvider{AdminDSN: "postgres://admin@localhost/db"}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-env",
		},
	}

	res, err := provider.Provision(context.Background(), env)
	assert.NoError(t, err)
	assert.Contains(t, res.SetupSQL, "CREATE SCHEMA IF NOT EXISTS %I")
	assert.Contains(t, res.SetupSQL, "CREATE TABLE IF NOT EXISTS %I.%I (LIKE public.%I INCLUDING ALL)")
	assert.Contains(t, res.SetupSQL, "SET LOCAL search_path TO preview_test_env, public;")
	assert.Contains(t, res.SetupSQL, "ALTER TABLE %I.%I ALTER COLUMN %I SET DEFAULT %s")
	assert.Contains(t, res.SetupSQL, "CREATE SEQUENCE IF NOT EXISTS %I.%I")

	// S1: DSN must use per-schema restricted role, NOT admin credentials
	assert.Contains(t, res.DSN, "diverge_preview_preview_test_env:")
	assert.Contains(t, res.DSN, "search_path=preview_test_env,public")
	assert.NotContains(t, res.DSN, "admin", "DSN must not contain admin credentials")
	assert.Contains(t, res.EnvVars["DATABASE_URL"], "diverge_preview_preview_test_env:")
	assert.NotContains(t, res.EnvVars["DATABASE_URL"], "admin", "DATABASE_URL must not contain admin credentials")
	assert.Equal(t, "preview_test_env", res.EnvVars["DIVERGE_PREVIEW_SCHEMA"])

	// Verify role creation SQL
	assert.Contains(t, res.SetupSQL, "CREATE ROLE")
	assert.Contains(t, res.SetupSQL, "GRANT USAGE ON SCHEMA")
	assert.Contains(t, res.SetupSQL, "GRANT ALL ON ALL TABLES IN SCHEMA")
}

func TestSchemaDatabaseProvider_Provision_EmptyName(t *testing.T) {
	provider := &SchemaDatabaseProvider{AdminDSN: "postgres://admin@localhost/db"}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "",
		},
	}

	_, err := provider.Provision(context.Background(), env)
	assert.ErrorIs(t, err, ErrInvalidSchemaName)
}

func TestSchemaDatabaseProvider_Provision_LongName(t *testing.T) {
	provider := &SchemaDatabaseProvider{AdminDSN: "postgres://admin@localhost/db"}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: strings.Repeat("a", 100),
		},
	}

	res, err := provider.Provision(context.Background(), env)
	assert.NoError(t, err)
	assert.Equal(t, 63, len(res.EnvVars["DIVERGE_PREVIEW_SCHEMA"]))
}

func TestSchemaDatabaseProvider_Provision_SpecialChars(t *testing.T) {
	provider := &SchemaDatabaseProvider{AdminDSN: "postgres://admin@localhost/db"}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my!@#$%^&*env",
		},
	}

	_, err := provider.Provision(context.Background(), env)
	assert.ErrorIs(t, err, ErrInvalidSchemaName)
}

func TestSchemaDatabaseProvider_Provision_DSN_WithQueryParam(t *testing.T) {
	provider := &SchemaDatabaseProvider{AdminDSN: "postgres://admin@localhost/db?sslmode=require"}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-env",
		},
	}

	res, err := provider.Provision(context.Background(), env)
	assert.NoError(t, err)
	assert.Contains(t, res.DSN, "sslmode=require")
}

type errorExecutor struct {
	err error
}

func (e *errorExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return nil, e.err
}

func TestSchemaDatabaseProvider_Provision_ExecutorHappy(t *testing.T) {
	mockExec := &recordingExecutor{}
	provider := &SchemaDatabaseProvider{AdminDSN: "postgres://admin@localhost/db", Executor: mockExec}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env"},
	}

	res, err := provider.Provision(context.Background(), env)
	assert.NoError(t, err)
	assert.True(t, mockExec.called)
	assert.Contains(t, mockExec.lastSQL, "CREATE SCHEMA IF NOT EXISTS")
	assert.True(t, res.Ready)
}

func TestSchemaDatabaseProvider_Provision_ExecutorError(t *testing.T) {
	provider := &SchemaDatabaseProvider{AdminDSN: "postgres://admin@localhost/db", Executor: &errorExecutor{err: context.DeadlineExceeded}}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env"},
	}

	res, err := provider.Provision(context.Background(), env)
	assert.NoError(t, err)
	assert.False(t, res.Ready)
	assert.Contains(t, res.Message, "failed to execute setup SQL:")
}

func TestSchemaDatabaseProvider_Provision_Concurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Each goroutine gets its own executor to avoid races
			mockExec := &recordingExecutor{}
			provider := &SchemaDatabaseProvider{AdminDSN: "postgres://admin@localhost/db", Executor: mockExec}
			env := &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "test-env"},
			}
			res, err := provider.Provision(context.Background(), env)
			assert.NoError(t, err)
			assert.True(t, res.Ready)
		}(i)
	}
	wg.Wait()
}

func TestSchemaDatabaseProvider_Provision_Idempotent(t *testing.T) {
	mockExec := &recordingExecutor{}
	provider := &SchemaDatabaseProvider{AdminDSN: "postgres://admin@localhost/db", Executor: mockExec}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env"},
	}

	res1, err := provider.Provision(context.Background(), env)
	assert.NoError(t, err)
	assert.True(t, res1.Ready)

	res2, err := provider.Provision(context.Background(), env)
	assert.NoError(t, err)
	assert.True(t, res2.Ready)

	// Assert both DSNs contain the same role name
	assert.Contains(t, res1.DSN, "diverge_preview_preview_test_env")
	assert.Contains(t, res2.DSN, "diverge_preview_preview_test_env")

	// Assert SetupSQL contains IF NOT EXISTS
	assert.Contains(t, res1.SetupSQL, "IF NOT EXISTS")
}

func TestSafeRoleName_Short(t *testing.T) {
	res := safeRoleName("test_env")
	assert.LessOrEqual(t, len(res), 63)
	assert.Equal(t, "diverge_preview_test_env", res)
}

func TestSafeRoleName_Long(t *testing.T) {
	// 60 chars
	schema := strings.Repeat("a", 60)
	res := safeRoleName(schema)
	assert.LessOrEqual(t, len(res), 63)
	assert.True(t, strings.HasPrefix(res, "diverge_preview_"))

	// Has hash suffix (8 hex chars)
	parts := strings.Split(res, "_")
	hashPart := parts[len(parts)-1]
	assert.Equal(t, 8, len(hashPart))
}

func TestSafeRoleName_Deterministic(t *testing.T) {
	schema := strings.Repeat("a", 60)
	res1 := safeRoleName(schema)
	res2 := safeRoleName(schema)
	assert.Equal(t, res1, res2)
}

func TestBuildWorkloadDSN_URLForm(t *testing.T) {
	adminDSN := "postgres://admin:pass@localhost/db?sslmode=disable"
	res := buildWorkloadDSN(adminDSN, "my_role", "my_pass", "my_schema")
	assert.Contains(t, res, "my_role")
	assert.Contains(t, res, "my_pass")
	assert.NotContains(t, res, "admin")
	assert.Contains(t, res, "search_path=my_schema")
}

func TestBuildWorkloadDSN_KeywordValueForm(t *testing.T) {
	adminDSN := "user=admin password=admin host=localhost dbname=db"
	res := buildWorkloadDSN(adminDSN, "my_role", "my_pass", "my_schema")
	assert.Contains(t, res, "user=my_role")
	assert.Contains(t, res, "password=my_pass")
	assert.Contains(t, res, "search_path=my_schema")
	assert.NotContains(t, res, "admin")
}
