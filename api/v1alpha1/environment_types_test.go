package v1alpha1

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEnvironmentDeepCopy(t *testing.T) {
	env := &Environment{
		Spec: EnvironmentSpec{
			Source: EnvironmentSource{
				Provider: "gitlab",
				Project:  "test-project",
			},
		},
	}

	copied := env.DeepCopy()
	if copied.Spec.Source.Provider != env.Spec.Source.Provider {
		t.Errorf("DeepCopy failed for Provider")
	}
}

// dnsLabelRegex matches a valid DNS-1123 label.
var dnsLabelRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func TestPreviewNamespace(t *testing.T) {
	tests := []struct {
		name         string
		envName      string
		envNamespace string
	}{
		{
			name:         "short name",
			envName:      "mr-42",
			envNamespace: "default",
		},
		{
			name:         "exactly at boundary",
			envName:      strings.Repeat("a", 55),
			envNamespace: "default",
		},
		{
			name:         "exceeds 63 chars gets truncated with hash",
			envName:      strings.Repeat("a", 100),
			envNamespace: "default",
		},
		{
			name:         "very long name",
			envName:      strings.Repeat("x", 200),
			envNamespace: "default",
		},
		{
			name:         "dotted name is sanitized",
			envName:      "my.dotted.env.name",
			envNamespace: "default",
		},
		{
			name:         "underscored name is sanitized",
			envName:      "my_underscored_name",
			envNamespace: "default",
		},
		{
			name:         "mixed special chars",
			envName:      "feat/BRANCH--name.v2_final",
			envNamespace: "product-clinical",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := &Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.envName,
					Namespace: tt.envNamespace,
				},
			}
			result := env.PreviewNamespace()

			assert.LessOrEqual(t, len(result), 63, "must be valid DNS label length")
			assert.True(t, strings.HasPrefix(result, "diverge-"), "must have diverge- prefix")
			assert.True(t, dnsLabelRegex.MatchString(result), "must be valid DNS-1123 label: %q", result)

			// Stability: same input always produces same output
			assert.Equal(t, result, env.PreviewNamespace(), "must be deterministic")
		})
	}
}

func TestPreviewNamespaceCollision(t *testing.T) {
	// Two different long names that would collide after truncation
	env1 := &Environment{}
	env1.Name = strings.Repeat("a", 60) + "1"
	env2 := &Environment{}
	env2.Name = strings.Repeat("a", 60) + "2"

	assert.NotEqual(t, env1.PreviewNamespace(), env2.PreviewNamespace(),
		"different names must produce different namespaces")
}

func TestPreviewNamespaceCrossNamespace(t *testing.T) {
	// Same name in different namespaces must produce different preview namespaces
	env1 := &Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mr-42",
			Namespace: "namespace-a",
		},
	}
	env2 := &Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mr-42",
			Namespace: "namespace-b",
		},
	}

	result1 := env1.PreviewNamespace()
	result2 := env2.PreviewNamespace()

	assert.NotEqual(t, result1, result2,
		"same name in different namespaces must produce different preview namespaces")

	// Both should still have the same prefix (from the env name)
	assert.True(t, strings.HasPrefix(result1, "diverge-mr-42-"))
	assert.True(t, strings.HasPrefix(result2, "diverge-mr-42-"))
}

func TestPreviewNamespaceDottedNames(t *testing.T) {
	env := &Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my.env.with.dots",
			Namespace: "default",
		},
	}

	result := env.PreviewNamespace()

	assert.True(t, dnsLabelRegex.MatchString(result), "dotted name must produce valid DNS label: %q", result)
	assert.NotContains(t, result, ".", "dots must be replaced")
	assert.Contains(t, result, "my-env-with-dots", "dots should become hyphens")
}
