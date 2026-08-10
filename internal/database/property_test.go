package database

import (
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"hegel.dev/go/hegel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSchemaNameIsDNSSafe(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		name := hegel.Draw(ht, hegel.Text())
		ns := hegel.Draw(ht, hegel.Text())

		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
		}

		schema, err := SchemaName(env)
		if err == nil {
			assert.Regexp(ht, "^[a-z][a-z0-9_]{0,62}$", schema, "schema name should match DNS requirements")
		}
	})
}

func TestSchemaNameIsInjectionSafe(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		name := hegel.Draw(ht, hegel.Text())
		ns := hegel.Draw(ht, hegel.Text())

		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
		}

		schema, err := SchemaName(env)
		if err == nil {
			unsafeChars := []string{";", "'", "--", "/*", "*/", "\"", "`"}
			for _, char := range unsafeChars {
				assert.NotContains(ht, schema, char, "schema name contains unsafe character")
			}
		}
	})
}
