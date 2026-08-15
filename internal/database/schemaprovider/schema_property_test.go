//go:build !no_schema

package schemaprovider

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSanitizeEnvName_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text())

		output, err := sanitizeEnvName(input)
		if err != nil {
			assert.ErrorIs(ht, err, ErrInvalidSchemaName)
			return
		}

		assert.LessOrEqual(ht, len(output), 55)
		matched, _ := regexp.MatchString(`^[a-z0-9][a-z0-9_-]*$`, output)
		assert.True(ht, matched, "Output %q contains invalid characters", output)
	})
}

func TestSanitizeEnvName_Deterministic(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text())

		out1, err1 := sanitizeEnvName(input)
		out2, err2 := sanitizeEnvName(input)

		assert.Equal(ht, err1, err2)
		assert.Equal(ht, out1, out2)
	})
}

func TestSchemaDatabaseProvider_Provision_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		envName := hegel.Draw(ht, hegel.Text())
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
			assert.Error(ht, err)
			return
		}

		assert.NoError(ht, err)
		expectedSchema := "preview_" + sanitized

		assert.Contains(ht, res.DSN, "search_path="+expectedSchema)
		assert.Equal(ht, expectedSchema, res.EnvVars["DIVERGE_PREVIEW_SCHEMA"])
		assert.Contains(ht, res.SetupSQL, expectedSchema)
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

var validEnvFirstChars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
var validEnvMidChars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "_", "-"}

func genValidEnvName(ht *hegel.T) string {
	length := hegel.Draw(ht, hegel.Integers(1, 51))
	first := hegel.Draw(ht, hegel.SampledFrom(validEnvFirstChars))
	if length == 1 {
		return first
	}
	rest := ""
	for i := 0; i < length-1; i++ {
		rest += hegel.Draw(ht, hegel.SampledFrom(validEnvMidChars))
	}
	return first + rest
}

func TestSchemaDatabaseProvider_Property_ExecutorCalledOnValidNames(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		envName := genValidEnvName(ht)

		rec := recordingExecutor{}
		provider := &SchemaDatabaseProvider{AdminDSN: "postgres://invalid:invalid@localhost:1/db", Executor: &rec}
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name: envName,
			},
		}

		res, err := provider.Provision(context.Background(), env)
		require.NoError(ht, err)

		assert.NotNil(ht, res)
		assert.True(ht, res.Ready)
		assert.True(ht, rec.called)
	})
}

var invalidChars1 = []string{"!", "@", "#", "$", "%", "^", "&", "*", "(", ")", " ", "\n", "\t"}
var invalidChars2 = []string{";", "'", "\"", "/", "\\"}

func genInvalidEnvName(ht *hegel.T) string {
	choice := hegel.Draw(ht, hegel.Integers(0, 2))
	if choice == 0 {
		return ""
	}
	var chars []string
	if choice == 1 {
		chars = invalidChars1
	} else {
		chars = invalidChars2
	}
	length := hegel.Draw(ht, hegel.Integers(1, 10))
	res := ""
	for i := 0; i < length; i++ {
		res += hegel.Draw(ht, hegel.SampledFrom(chars))
	}
	return res
}

func TestSchemaDatabaseProvider_Property_ExecutorNotCalledOnInvalidNames(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		envName := genInvalidEnvName(ht)

		rec := recordingExecutor{}
		provider := &SchemaDatabaseProvider{AdminDSN: "postgres://invalid:invalid@localhost:1/db", Executor: &rec}
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name: envName,
			},
		}

		res, err := provider.Provision(context.Background(), env)

		// It should fail at validation phase immediately, before opening database
		assert.Nil(ht, res)
		assert.ErrorIs(ht, err, ErrInvalidSchemaName)
		assert.False(ht, rec.called)
	})
}
