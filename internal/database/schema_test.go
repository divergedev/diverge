package database

import (
	"context"
	"strings"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSchemaDatabaseProvider_Sanitize(t *testing.T) {
	name := sanitizeEnvName("my-test-env-123!")
	assert.Equal(t, "my_test_env_123", name)
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
	assert.Contains(t, res.SetupSQL, "CREATE SCHEMA IF NOT EXISTS preview_test_env")
	assert.Contains(t, res.SetupSQL, "LIKE public.")
	assert.Equal(t, "postgres://admin@localhost/db?search_path=preview_test_env,public", res.DSN)
	assert.Equal(t, "postgres://admin@localhost/db?search_path=preview_test_env,public", res.EnvVars["DATABASE_URL"])
	assert.Equal(t, "preview_test_env", res.EnvVars["DIVERGE_PREVIEW_SCHEMA"])
}

func TestSchemaDatabaseProvider_Provision_EmptyName(t *testing.T) {
	provider := &SchemaDatabaseProvider{AdminDSN: "postgres://admin@localhost/db"}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "",
		},
	}

	res, err := provider.Provision(context.Background(), env)
	assert.NoError(t, err)
	assert.Equal(t, "preview_", res.EnvVars["DIVERGE_PREVIEW_SCHEMA"])
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

	res, err := provider.Provision(context.Background(), env)
	assert.NoError(t, err)
	assert.Equal(t, "preview_myenv", res.EnvVars["DIVERGE_PREVIEW_SCHEMA"])
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
	assert.Equal(t, "postgres://admin@localhost/db?sslmode=require&search_path=preview_test_env,public", res.DSN)
}
