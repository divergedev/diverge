package features

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	Providers.Register("noop", registry.Provider[FeatureProvider]{
		Create: func(deps registry.Deps) (FeatureProvider, error) {
			return &NoopProvider{}, nil
		},
		Description: "No-op feature flag provider (disables feature provisioning)",
	})
	Providers.Register("none", registry.Provider[FeatureProvider]{
		Create: func(deps registry.Deps) (FeatureProvider, error) {
			return &NoopProvider{}, nil
		},
		Description: "None feature flag provider (disables feature provisioning)",
	})
}

// NoopProvider is a no-op implementation of FeatureProvider.
type NoopProvider struct{}

// Type returns "noop".
func (n *NoopProvider) Type() string {
	return "noop"
}

// Provision performs no operations and reports ready.
func (n *NoopProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*FeatureResult, error) {
	return &FeatureResult{
		ProviderType: "noop",
		Message:      "Feature flag management disabled (noop)",
	}, nil
}

// Teardown performs no operations.
func (n *NoopProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	return nil
}

// Status always reports ready.
func (n *NoopProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*FeatureStatus, error) {
	return &FeatureStatus{
		Ready:   true,
		Message: "Noop provider ready",
	}, nil
}
