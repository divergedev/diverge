// Package async provides interfaces for async infrastructure provisioning.
package async

import (
	"context"
	"errors"

	v1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

// ErrNilProvisionResult is returned when a provisioner returns nil result without an error.
var ErrNilProvisionResult = errors.New("provisioner returned nil result")

// ProvisionResult contains the result of provisioning an async route.
type ProvisionResult struct {
	// ResolvedTarget is the provisioned resource name (e.g., "preview-123-payments" task queue).
	ResolvedTarget string
	// EnvVars are environment variables to inject into preview pods.
	EnvVars map[string]string
}

// Provisioner provisions and tears down async infrastructure for preview environments.
type Provisioner interface {
	// Provision creates async infrastructure for the given route spec.
	Provision(ctx context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) (*ProvisionResult, error)
	// Teardown removes async infrastructure for the given route spec.
	Teardown(ctx context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) error
	// Name returns the provisioner name.
	Name() string
}
