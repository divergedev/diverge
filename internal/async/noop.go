package async

import (
	"context"
	"fmt"

	v1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

// NoopProvisioner is a no-op provisioner that returns the target unchanged.
type NoopProvisioner struct{}

func (n *NoopProvisioner) Name() string { return "noop" }

func (n *NoopProvisioner) Provision(_ context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) (*ProvisionResult, error) {
	envVars := make(map[string]string)
	target := fmt.Sprintf("%s-%s", route.Target, env.Name)

	if len(route.EnvVarMapping) == 0 {
		if defaultVar := v1alpha1.DefaultEnvVarForProtocol(route.Protocol); defaultVar != "" {
			envVars[defaultVar] = target
		}
	}

	return &ProvisionResult{
		ResolvedTarget: target,
		EnvVars:        envVars,
	}, nil
}

func (n *NoopProvisioner) Teardown(_ context.Context, _ *v1alpha1.Environment, _ v1alpha1.AsyncRouteSpec) error {
	return nil
}
