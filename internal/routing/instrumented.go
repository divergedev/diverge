package routing

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/metrics"
)

// InstrumentedRouter wraps a Router with Prometheus metrics.
type InstrumentedRouter struct {
	Inner Router
	Mode  string
}

func (r *InstrumentedRouter) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	timer := prometheus.NewTimer(metrics.RoutingReconcileDuration.WithLabelValues(r.Mode))
	defer timer.ObserveDuration()
	err := r.Inner.Reconcile(ctx, env)
	if err != nil {
		metrics.SubsystemErrors.WithLabelValues("routing", "reconcile").Inc()
	}
	return err
}

func (r *InstrumentedRouter) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	err := r.Inner.Teardown(ctx, env)
	if err != nil {
		metrics.SubsystemErrors.WithLabelValues("routing", "teardown").Inc()
	}
	return err
}

func (r *InstrumentedRouter) GetExternalURL(env *v1alpha1.Environment) string {
	return r.Inner.GetExternalURL(env)
}
