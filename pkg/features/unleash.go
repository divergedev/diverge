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
	Providers.Register("unleash", registry.Provider[FeatureProvider]{
		Create: func(deps registry.Deps) (FeatureProvider, error) {
			return NewUnleashProvider(deps.Client, deps.Scheme, deps.Logger), nil
		},
		Description: "Unleash feature flag provider (ephemeral preview environments, stub)",
	})
}

// UnleashProvider is a feature flag provider stub for Unleash.
// Full implementation is tracked in https://github.com/divergedev/diverge/issues/268.
type UnleashProvider struct {
	client client.Client
	scheme *runtime.Scheme
	logger logr.Logger
}

// NewUnleashProvider creates a new UnleashProvider stub.
func NewUnleashProvider(c client.Client, scheme *runtime.Scheme, logger logr.Logger) *UnleashProvider {
	return &UnleashProvider{
		client: c,
		scheme: scheme,
		logger: logger,
	}
}

// Type returns "unleash".
func (p *UnleashProvider) Type() string {
	return "unleash"
}

// Provision sets up preview configuration for Unleash.
func (p *UnleashProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*FeatureResult, error) {
	appName := fmt.Sprintf("diverge-%s", env.Name)
	p.logger.Info("Unleash feature provider stub provisioned", "environment", env.Name, "appName", appName)

	return &FeatureResult{
		ProviderType: "unleash",
		EnvVars: map[string]string{
			"UNLEASH_APP_NAME":    appName,
			"UNLEASH_ENVIRONMENT": env.Name,
		},
		Message: "Unleash provider stub initialized (tracking issue https://github.com/divergedev/diverge/issues/268)",
	}, nil
}

// Teardown cleans up Unleash ephemeral resources.
func (p *UnleashProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	p.logger.Info("Unleash feature provider stub teardown", "environment", env.Name)
	return nil
}

// Status returns readiness of the Unleash provider stub.
func (p *UnleashProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*FeatureStatus, error) {
	return &FeatureStatus{
		Ready:   true,
		Message: "Unleash provider stub active (issue #268)",
	}, nil
}
