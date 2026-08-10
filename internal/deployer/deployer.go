// Package deployer provides pluggable service deployment backends.
package deployer

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// Deployer deploys and tears down services for a preview environment.
type Deployer interface {
	Deploy(ctx context.Context, env *v1alpha1.Environment) error
	Teardown(ctx context.Context, env *v1alpha1.Environment) error
	// Status returns the current deployment status for the environment.
	// An empty slice indicates no active deployments remain.
	Status(ctx context.Context, env *v1alpha1.Environment) ([]ServiceStatus, error)
}
