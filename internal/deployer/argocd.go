package deployer

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/argocd"
)

// ArgoDeployer implements Deployer using Argo CD Application CRs.
// It delegates to the existing argocd.Client and argocd.Generator.
type ArgoDeployer struct {
	client         argocd.Applicator
	generator      *argocd.Generator
	serviceConfigs map[string]argocd.ServiceConfig
}

// NewArgoDeployer creates a new ArgoDeployer.
func NewArgoDeployer(client argocd.Applicator, generator *argocd.Generator, serviceConfigs map[string]argocd.ServiceConfig) *ArgoDeployer {
	if serviceConfigs == nil {
		serviceConfigs = make(map[string]argocd.ServiceConfig)
	}
	return &ArgoDeployer{
		client:         client,
		generator:      generator,
		serviceConfigs: serviceConfigs,
	}
}

// Deploy creates or updates Argo CD Application CRs for the environment.
func (d *ArgoDeployer) Deploy(ctx context.Context, env *v1alpha1.Environment) error {
	changedServices := env.Spec.Deploy.ChangedServices

	// Create Application CRs using the generator
	apps, err := d.generator.Generate(env, changedServices, d.serviceConfigs)
	if err != nil {
		return err
	}

	// Apply them using the client
	return d.client.ApplyApplications(ctx, apps)
}

// Teardown deletes the Argo CD Application CRs for the environment.
func (d *ArgoDeployer) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	return d.client.DeleteApplicationsForEnvironment(ctx, env.Name, env.Namespace)
}

// Status returns the sync status of ArgoCD Applications for this environment.
func (d *ArgoDeployer) Status(ctx context.Context, env *v1alpha1.Environment) ([]argocd.ApplicationStatus, error) {
	return d.client.GetSyncStatus(ctx, env.Name, env.Namespace)
}
