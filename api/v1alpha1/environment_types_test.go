package v1alpha1

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestPreviewNamespace(t *testing.T) {
	tests := []struct {
		name     string
		envName  string
		expected string
	}{
		{
			name:     "short name",
			envName:  "mr-42",
			expected: "diverge-mr-42",
		},
		{
			name:     "exactly 63 chars",
			envName:  strings.Repeat("a", 55), // "diverge-" is 8 chars + 55 = 63
			expected: "diverge-" + strings.Repeat("a", 55),
		},
		{
			name:    "exceeds 63 chars gets truncated with hash",
			envName: strings.Repeat("a", 56), // "diverge-" + 56 = 64 > 63
		},
		{
			name:    "very long name",
			envName: strings.Repeat("x", 200),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := &Environment{}
			env.Name = tt.envName
			result := env.PreviewNamespace()

			assert.LessOrEqual(t, len(result), 63, "must be valid DNS label length")
			assert.True(t, strings.HasPrefix(result, "diverge-"), "must have diverge- prefix")

			if tt.expected != "" {
				assert.Equal(t, tt.expected, result)
			}

			// Stability: same input always produces same output
			assert.Equal(t, result, env.PreviewNamespace(), "must be deterministic")
		})
	}
}

func TestPreviewNamespaceCollision(t *testing.T) {
	// Two different names that would collide after truncation should produce different results
	env1 := &Environment{}
	env1.Name = strings.Repeat("a", 60) + "1"
	env2 := &Environment{}
	env2.Name = strings.Repeat("a", 60) + "2"

	assert.NotEqual(t, env1.PreviewNamespace(), env2.PreviewNamespace(), "different names must produce different namespaces")
}
