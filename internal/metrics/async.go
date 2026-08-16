package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// AsyncProvisionDurationSeconds measures how long async provisioning takes.
	AsyncProvisionDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "diverge",
			Name:      "async_provision_duration_seconds",
			Help:      "Async provisioning duration in seconds",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"protocol", "result"},
	)

	// AsyncProvisionErrorsTotal counts async provisioning errors.
	AsyncProvisionErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "diverge",
			Name:      "async_provision_errors_total",
			Help:      "Total number of async provisioning errors",
		},
		[]string{"protocol"},
	)

	// AsyncRoutesActive gauges the number of active async routes.
	AsyncRoutesActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "diverge",
			Name:      "async_routes_active",
			Help:      "Number of currently active async routes",
		},
		[]string{"protocol", "namespace"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		AsyncProvisionDurationSeconds,
		AsyncProvisionErrorsTotal,
		AsyncRoutesActive,
	)
}
