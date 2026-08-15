package database

import (
	"context"
	"regexp"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"pgregory.net/rapid"
)

func TestSanitizeEnvName_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		input := rapid.String().Draw(t, "input")

		output, err := sanitizeEnvName(input)
		if err != nil {
			assert.ErrorIs(t, err, ErrInvalidSchemaName)
			return
		}

		assert.LessOrEqual(t, len(output), 55)
		matched, _ := regexp.MatchString(`^[a-z0-9][a-z0-9_-]*$`, output)
		assert.True(t, matched, "Output %q contains invalid characters", output)
	})
}

func TestSanitizeEnvName_Deterministic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		input := rapid.String().Draw(t, "input")

		out1, err1 := sanitizeEnvName(input)
		out2, err2 := sanitizeEnvName(input)

		assert.Equal(t, err1, err2)
		assert.Equal(t, out1, out2)
	})
}

func TestSchemaDatabaseProvider_Provision_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		envName := rapid.String().Draw(t, "envName")
		adminDSN := "postgres://admin@localhost/db"

		provider := &SchemaDatabaseProvider{AdminDSN: adminDSN}
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name: envName,
			},
		}

		res, err := provider.Provision(context.Background(), env)
		sanitized, sErr := sanitizeEnvName(envName)

		if sErr != nil {
			assert.Error(t, err)
			return
		}

		assert.NoError(t, err)
		expectedSchema := "preview_" + sanitized

		assert.Contains(t, res.DSN, "search_path="+expectedSchema)
		assert.Equal(t, expectedSchema, res.EnvVars["DIVERGE_PREVIEW_SCHEMA"])
		assert.Contains(t, res.SetupSQL, expectedSchema)
	})
}

func TestSchemaDatabaseProvider_Property_ExecutorCalledOnValidNames(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate valid env names
		envName := rapid.StringMatching(`^[a-z0-9][a-z0-9_-]{0,50}$`).Draw(rt, "envName")

		provider := &SchemaDatabaseProvider{AdminDSN: "postgres://invalid:invalid@localhost:1/db"}
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name: envName,
			},
		}

		res, err := provider.Provision(context.Background(), env)
		require.NoError(t, err)

		// It should fail at execution phase, not validation phase
		assert.NotNil(t, res)
		assert.False(t, res.Ready)
		assert.Contains(t, res.Message, "failed to execute setup SQL")
		// The requirement mentioned checking noopSQLExecutor.lastDSN, but since it doesn't
		// exist, we verify the execution attempted by checking the execution error message
	})
}

func TestSchemaDatabaseProvider_Property_ExecutorNotCalledOnInvalidNames(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate invalid env names
		invalidStrGen := rapid.StringMatching(`[!@#\$%\^&\*\(\) \n\t]+`)
		envName := invalidStrGen.Draw(rt, "envName")

		provider := &SchemaDatabaseProvider{AdminDSN: "postgres://invalid:invalid@localhost:1/db"}
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name: envName,
			},
		}

		res, err := provider.Provision(context.Background(), env)

		// It should fail at validation phase immediately, before opening database
		assert.Nil(t, res)
		assert.ErrorIs(t, err, ErrInvalidSchemaName)
	})
}
