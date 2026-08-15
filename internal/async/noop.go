package async

import (
	"context"
	"fmt"

	v1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

// NoopProvisioner is a no-op provisioner that returns the target unchanged.
type NoopProvisioner struct{}

// Name returns the provisioner name.
func (n *NoopProvisioner) Name() string { return "noop" }

// Provision returns a resolved target of "<target>-<envName>" and populates
// default or custom environment variables without creating real infrastructure.
func (n *NoopProvisioner) Provision(_ context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) (*ProvisionResult, error) {
	envVars := make(map[string]string)
	target := fmt.Sprintf("%s-%s", route.Target, env.Name)

	if len(route.EnvVarMapping) == 0 {
		if defaultVar := v1alpha1.DefaultEnvVarForProtocol(route.Protocol); defaultVar != "" {
			envVars[defaultVar] = target
		}
	} else {
		for envVar, tmpl := range route.EnvVarMapping {
			if tmpl == "{{ .ResolvedTarget }}" || tmpl == "" {
				envVars[envVar] = target
			}
		}
	}

	return &ProvisionResult{
		ResolvedTarget: target,
		EnvVars:        envVars,
	}, nil
}

// Teardown is a no-op since no real infrastructure was created.
func (n *NoopProvisioner) Teardown(_ context.Context, _ *v1alpha1.Environment, _ v1alpha1.AsyncRouteSpec) error {
	return nil
}
