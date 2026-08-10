package deployer

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/argocd"
)

// NoopDeployer is a Deployer that does nothing.
// Used when deployment is managed externally or for testing.
type NoopDeployer struct{}

// Deploy does nothing.
func (n *NoopDeployer) Deploy(ctx context.Context, env *v1alpha1.Environment) error {
	return nil
}

// Teardown does nothing.
func (n *NoopDeployer) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	return nil
}

// Status returns no active deployments.
func (n *NoopDeployer) Status(ctx context.Context, env *v1alpha1.Environment) ([]argocd.ApplicationStatus, error) {
	return nil, nil
}
