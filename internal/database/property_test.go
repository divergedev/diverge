package database

import (
	"regexp"
	"strings"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"pgregory.net/rapid"
)

func TestSchemaNameIsDNSSafe(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.String().Draw(t, "name")
		ns := rapid.String().Draw(t, "ns")

		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
		}

		schema, err := SchemaName(env)
		if err == nil {
			if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`).MatchString(schema) {
				t.Fatalf("schema name %q does not match requirements", schema)
			}
		}
	})
}

func TestSchemaNameIsInjectionSafe(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.String().Draw(t, "name")
		ns := rapid.String().Draw(t, "ns")

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
				if strings.Contains(schema, char) {
					t.Fatalf("schema name %q contains unsafe character %q", schema, char)
				}
			}
		}
	})
}
