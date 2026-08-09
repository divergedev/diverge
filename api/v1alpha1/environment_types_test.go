package v1alpha1

import (
	"testing"
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
