package deployer

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/argocd"
	"github.com/divergedev/diverge/internal/metrics"
)

// InstrumentedDeployer wraps a Deployer with Prometheus metrics.
type InstrumentedDeployer struct {
	Inner Deployer
	Name  string
}

func (d *InstrumentedDeployer) Deploy(ctx context.Context, env *v1alpha1.Environment) error {
	timer := prometheus.NewTimer(metrics.DeployDuration.WithLabelValues(d.Name))
	defer timer.ObserveDuration()
	err := d.Inner.Deploy(ctx, env)
	if err != nil {
		metrics.SubsystemErrors.WithLabelValues("deployer", "deploy").Inc()
	}
	return err
}

func (d *InstrumentedDeployer) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	err := d.Inner.Teardown(ctx, env)
	if err != nil {
		metrics.SubsystemErrors.WithLabelValues("deployer", "teardown").Inc()
	}
	return err
}

func (d *InstrumentedDeployer) Status(ctx context.Context, env *v1alpha1.Environment) ([]argocd.ApplicationStatus, error) {
	status, err := d.Inner.Status(ctx, env)
	if err != nil {
		metrics.SubsystemErrors.WithLabelValues("deployer", "status").Inc()
	}
	return status, err
}
