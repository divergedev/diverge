package v1alpha1

import (
	"testing"

	"hegel.dev/go/hegel"
)

func TestEnvironmentDeepCopyProperty(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		provider := hegel.Draw(ht, hegel.SampledFrom([]string{"gitlab", "github"}))
		env := &Environment{
			Spec: EnvironmentSpec{
				Source: EnvironmentSource{
					Provider: provider,
				},
			},
		}

		copied := env.DeepCopy()
		if copied.Spec.Source.Provider != env.Spec.Source.Provider {
			t.Errorf("DeepCopy failed for provider %s", provider)
		}
		if copied == env {
			t.Errorf("DeepCopy returned same pointer")
		}
	})
}
