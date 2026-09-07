package features

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	Providers.Register("flagsmith", registry.Provider[FeatureProvider]{
		Create: func(deps registry.Deps) (FeatureProvider, error) {
			return NewFlagsmithProvider(deps.Client, deps.Scheme, deps.Logger), nil
		},
		Description: "Flagsmith feature flag provider (ephemeral preview environments, stub)",
	})
}

// FlagsmithProvider is a feature flag provider stub for Flagsmith.
// Full implementation is tracked in https://github.com/divergedev/diverge/issues/267.
type FlagsmithProvider struct {
	client client.Client
	scheme *runtime.Scheme
	logger logr.Logger
}

// NewFlagsmithProvider creates a new FlagsmithProvider stub.
func NewFlagsmithProvider(c client.Client, scheme *runtime.Scheme, logger logr.Logger) *FlagsmithProvider {
	return &FlagsmithProvider{
		client: c,
		scheme: scheme,
		logger: logger,
	}
}

// Type returns "flagsmith".
func (p *FlagsmithProvider) Type() string {
	return "flagsmith"
}

// Provision sets up preview configuration for Flagsmith.
func (p *FlagsmithProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*FeatureResult, error) {
	envKey := fmt.Sprintf("preview-%s", env.Name)
	p.logger.Info("Flagsmith feature provider stub provisioned", "environment", env.Name, "envKey", envKey)

	return &FeatureResult{
		ProviderType: "flagsmith",
		EnvVars: map[string]string{
			"FLAGSMITH_ENVIRONMENT_KEY": envKey,
		},
		Message: "Flagsmith provider stub initialized (tracking issue https://github.com/divergedev/diverge/issues/267)",
	}, nil
}

// Teardown cleans up Flagsmith ephemeral resources.
func (p *FlagsmithProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	p.logger.Info("Flagsmith feature provider stub teardown", "environment", env.Name)
	return nil
}

// Status returns readiness of the Flagsmith provider stub.
func (p *FlagsmithProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*FeatureStatus, error) {
	return &FeatureStatus{
		Ready:   true,
		Message: "Flagsmith provider stub active (issue #267)",
	}, nil
}
