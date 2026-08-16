package server

import (
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestMapper_Environment(t *testing.T) {
	tests := []struct {
		name string
		env  v1alpha1.Environment
	}{
		{
			name: "basic mapping",
			env: v1alpha1.Environment{
				Spec: v1alpha1.EnvironmentSpec{
					Deploy: v1alpha1.EnvironmentDeploy{
						Mode: "delta",
					},
				},
			},
		},
		{
			name: "empty mapping",
			env:  v1alpha1.Environment{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dom, err := CRDEnvToDomain(&tc.env)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			crd, err := DomainEnvToCRD(dom)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if crd.Spec.Deploy.Mode != tc.env.Spec.Deploy.Mode {
				t.Fatalf("mapped values differ: expected Mode=%v, got Mode=%v", tc.env.Spec.Deploy.Mode, crd.Spec.Deploy.Mode)
			}
		})
	}
}
