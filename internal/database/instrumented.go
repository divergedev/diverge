package database

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/metrics"
	pkgdb "github.com/divergedev/diverge/pkg/database"
)

// InstrumentedDatabaseProvider wraps a DatabaseProvider with Prometheus metrics.
type InstrumentedDatabaseProvider struct {
	Inner pkgdb.DatabaseProvider
	Mode  string
}

// Provision performs its designated operation.
func (p *InstrumentedDatabaseProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*pkgdb.DatabaseResult, error) {
	timer := prometheus.NewTimer(metrics.DatabaseProvisionDuration.WithLabelValues(p.Mode))
	defer timer.ObserveDuration()
	status, err := p.Inner.Provision(ctx, env)
	if err != nil {
		metrics.SubsystemErrors.WithLabelValues("database", "provision").Inc()
	}
	return status, err
}

// Teardown performs its designated operation.
func (p *InstrumentedDatabaseProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	err := p.Inner.Teardown(ctx, env)
	if err != nil {
		metrics.SubsystemErrors.WithLabelValues("database", "teardown").Inc()
	}
	return err
}

// Status performs its designated operation.
func (p *InstrumentedDatabaseProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*pkgdb.DatabaseStatus, error) {
	status, err := p.Inner.Status(ctx, env)
	if err != nil {
		metrics.SubsystemErrors.WithLabelValues("database", "status").Inc()
	}
	return status, err
}
