package database

import (
	"context"
	"regexp"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"hegel.dev/go/hegel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSanitizeEnvName_ValidPGIdentifier(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text())

		output := sanitizeEnvName(input)

		assert.LessOrEqual(t, len(output), 55)

		matched, _ := regexp.MatchString(`^[a-zA-Z0-9_]*$`, output)
		assert.True(t, matched, "Output %q contains invalid characters", output)
	})
}

func TestSanitizeEnvName_Deterministic(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text())

		output1 := sanitizeEnvName(input)
		output2 := sanitizeEnvName(input)

		assert.Equal(t, output1, output2)
	})
}

func TestSanitizeEnvName_PreservesAlphanumeric(t *testing.T) {
	alphanumericGen := hegel.Filter(hegel.Text(), func(s string) bool {
		if len(s) > 55 {
			return false
		}
		matched, _ := regexp.MatchString(`^[a-z0-9]*$`, s)
		return matched
	})

	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, alphanumericGen)

		output := sanitizeEnvName(input)
		assert.Equal(t, input, output)
	})
}

func TestSchemaDatabaseProvider_Provision_SchemaNameInDSN(t *testing.T) {
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
		assert.NoError(t, err)

		sanitized := sanitizeEnvName(envName)
		expectedSchema := "preview_" + sanitized

		assert.Contains(t, res.DSN, "search_path="+expectedSchema)
		assert.Equal(t, expectedSchema, res.EnvVars["DIVERGE_PREVIEW_SCHEMA"])
	})
}

func TestSchemaDatabaseProvider_Provision_SetupSQL_ContainsSchema(t *testing.T) {
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
		assert.NoError(t, err)

		sanitized := sanitizeEnvName(envName)
		expectedSchema := "preview_" + sanitized

		assert.Contains(t, res.SetupSQL, expectedSchema)
	})
}
