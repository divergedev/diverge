package deployer

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// OTelAnnotationDeployer wraps a Deployer and injects OTel Operator
// auto-instrumentation annotations into deployed workloads.
type OTelAnnotationDeployer struct {
	Inner       Deployer
	Annotations map[string]string // e.g. {"instrumentation.opentelemetry.io/inject-java": "true"}
}

// Deploy performs its designated operation, injecting annotations into the Environment
// so that inner deployers can propagate them to workloads.
func (d *OTelAnnotationDeployer) Deploy(ctx context.Context, env *v1alpha1.Environment) error {
	if len(d.Annotations) > 0 {
		if env.Annotations == nil {
			env.Annotations = make(map[string]string)
		}
		for k, v := range d.Annotations {
			env.Annotations[k] = v
		}
	}
	return d.Inner.Deploy(ctx, env)
}

// Teardown delegates to the inner deployer.
func (d *OTelAnnotationDeployer) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	return d.Inner.Teardown(ctx, env)
}

// Status delegates to the inner deployer.
func (d *OTelAnnotationDeployer) Status(ctx context.Context, env *v1alpha1.Environment) ([]ServiceStatus, error) {
	return d.Inner.Status(ctx, env)
}
