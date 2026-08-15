//go:build !no_temporal

package async

import (
	"context"
	"fmt"

	v1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

// TemporalProvisioner provisions Temporal task queues for preview environments.
// Task queues in Temporal are lazy — they auto-create when a worker polls.
// This provisioner generates deterministic queue names and injects them as env vars.
type TemporalProvisioner struct {
	// Namespace is the Temporal namespace to use.
	Namespace string
}

// Name returns the provisioner name.
func (t *TemporalProvisioner) Name() string { return "temporal" }

// Provision generates a preview-scoped task queue name. Temporal task queues
// auto-create on first worker poll, so no actual provisioning is needed.
func (t *TemporalProvisioner) Provision(_ context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) (*ProvisionResult, error) {
	// Deterministic queue name: <target>-<env-name>
	target := fmt.Sprintf("%s-%s", route.Target, env.Name)

	envVars := make(map[string]string)
	if len(route.EnvVarMapping) == 0 {
		envVars["TEMPORAL_TASK_QUEUE"] = target
		if t.Namespace != "" {
			envVars["TEMPORAL_NAMESPACE"] = t.Namespace
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

// Teardown is a no-op for Temporal — stale task queues are harmless and
// are garbage-collected by Temporal when no workers are polling.
func (t *TemporalProvisioner) Teardown(_ context.Context, _ *v1alpha1.Environment, _ v1alpha1.AsyncRouteSpec) error {
	return nil
}
