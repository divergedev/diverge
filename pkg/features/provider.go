// Package features defines the public API for Diverge feature flag providers.
package features

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// FeatureProvider manages feature flags and remote configuration for preview environments.
type FeatureProvider interface {
	// Type returns the provider type identifier (e.g. "configmap", "flipt", "flagsmith", "unleash", "noop").
	Type() string

	// Provision configures or creates ephemeral feature rules for the preview environment.
	Provision(ctx context.Context, env *v1alpha1.Environment) (*FeatureResult, error)

	// Teardown cleans up ephemeral rules when the preview environment is destroyed.
	Teardown(ctx context.Context, env *v1alpha1.Environment) error

	// Status returns the current health or readiness of feature flag provisioning.
	Status(ctx context.Context, env *v1alpha1.Environment) (*FeatureStatus, error)
}

// FeatureResult contains the outcome of a Provision call.
type FeatureResult struct {
	// ProviderType indicates the active provider.
	ProviderType string

	// ConfigMapName is the name of any ConfigMap created to store flag configurations.
	ConfigMapName string

	// EnvVars are environment variables to inject into preview pods.
	EnvVars map[string]string

	// Message is an informative description of the provisioned state.
	Message string
}

// FeatureStatus represents the current state of feature management for an environment.
type FeatureStatus struct {
	// Ready indicates whether feature flag rules are successfully provisioned.
	Ready bool

	// Message describes the current status or error.
	Message string
}
