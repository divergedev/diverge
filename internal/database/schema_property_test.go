package database

import (
	"context"
	"database/sql"
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

type recordingExecutor struct {
	called  bool
	lastSQL string
}

func (r *recordingExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	r.called = true
	r.lastSQL = query
	return nil, nil
}

func TestSchemaDatabaseProvider_Property_ExecutorCalledOnValidNames(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate valid env names
		envName := rapid.StringMatching(`^[a-z0-9][a-z0-9_-]{0,50}$`).Draw(rt, "envName")

		rec := recordingExecutor{}
		provider := &SchemaDatabaseProvider{AdminDSN: "postgres://invalid:invalid@localhost:1/db", Executor: &rec}
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name: envName,
			},
		}

		res, err := provider.Provision(context.Background(), env)
		require.NoError(t, err)

		assert.NotNil(t, res)
		assert.True(t, res.Ready)
		assert.True(t, rec.called)
	})
}

func TestSchemaDatabaseProvider_Property_ExecutorNotCalledOnInvalidNames(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate invalid env names
		invalidStrGen := rapid.OneOf(
			rapid.StringMatching(`[!@#\$%\^&\*\(\) \n\t]+`),
			rapid.StringMatching(`[;'"/\\]+`),
			rapid.Just(""),
		)
		envName := invalidStrGen.Draw(rt, "envName")

		rec := recordingExecutor{}
		provider := &SchemaDatabaseProvider{AdminDSN: "postgres://invalid:invalid@localhost:1/db", Executor: &rec}
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name: envName,
			},
		}

		res, err := provider.Provision(context.Background(), env)

		// It should fail at validation phase immediately, before opening database
		assert.Nil(t, res)
		assert.ErrorIs(t, err, ErrInvalidSchemaName)
		assert.False(t, rec.called)
	})
}
