package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	asyncProvisionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "diverge",
		Subsystem: "controller",
		Name:      "async_provisions_total",
		Help:      "Total number of async route provisions by protocol and result",
	}, []string{"protocol", "result"})

	asyncProvisionDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "diverge",
		Subsystem: "controller",
		Name:      "async_provision_duration_seconds",
		Help:      "Duration of async route provisioning in seconds",
		Buckets:   prometheus.DefBuckets,
	}, []string{"protocol"})

	asyncTeardownsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "diverge",
		Subsystem: "controller",
		Name:      "async_teardowns_total",
		Help:      "Total number of async route teardowns by protocol and result",
	}, []string{"protocol", "result"})

	asyncActiveRoutes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "diverge",
		Subsystem: "controller",
		Name:      "async_active_routes",
		Help:      "Number of currently active async routes by protocol",
	}, []string{"protocol"})
)

func init() {
	crmetrics.Registry.MustRegister(
		asyncProvisionsTotal,
		asyncProvisionDuration,
		asyncTeardownsTotal,
		asyncActiveRoutes,
	)
}
