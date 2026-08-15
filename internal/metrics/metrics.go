// Package metrics defines and registers Prometheus metrics for all Diverge
// subsystems. Metrics use the "diverge_" prefix and are registered with
// controller-runtime's default registry (exposed on :8080/metrics).
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	EnvironmentsActive = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "diverge",
			Name:      "environments_active",
			Help:      "Number of active preview environments",
		},
	)

	PreviewGroupsActive = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "diverge",
			Name:      "previewgroups_active",
			Help:      "Number of active preview groups",
		},
	)

	ReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "diverge",
			Name:      "reconcile_total",
			Help:      "Reconciliation operations by controller and result",
		},
		[]string{"controller", "result"},
	)

	ReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "diverge",
			Name:      "reconcile_duration_seconds",
			Help:      "Reconcile duration",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"controller"},
	)

	DeploymentDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "diverge",
			Name:      "deployment_duration_seconds",
			Help:      "Deployment create latency",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"deployer"},
	)

	RoutesConfigured = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "diverge",
			Name:      "routes_configured",
			Help:      "Routes configured by type",
		},
		[]string{"type"},
	)

	EnvironmentTTLRemaining = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "diverge_environment_ttl_remaining_seconds",
			Help: "Seconds until TTL expiry for each environment",
		},
		[]string{"environment", "namespace"},
	)

	ReconcileOutcomes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "diverge_reconcile_outcomes_total",
			Help: "Reconciliation outcomes by result",
		},
		[]string{"result"},
	)

	EnvironmentTransitions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "diverge_environment_transitions_total",
			Help: "Environment phase transitions",
		},
		[]string{"from_phase", "to_phase", "provider"},
	)

	SubsystemErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "diverge_subsystem_errors_total",
			Help: "Errors by subsystem",
		},
		[]string{"subsystem", "operation"},
	)

	DatabaseProvisionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "diverge_database_provision_duration_seconds",
			Help:    "Time to provision database resources",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
		},
		[]string{"mode"},
	)

	DeployDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "diverge_deploy_duration_seconds",
			Help:    "Time to deploy services",
			Buckets: prometheus.ExponentialBuckets(0.5, 2, 10),
		},
		[]string{"deployer"},
	)

	RoutingReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "diverge_routing_reconcile_duration_seconds",
			Help:    "Time to reconcile routing rules",
			Buckets: prometheus.ExponentialBuckets(0.05, 2, 8),
		},
		[]string{"mode"},
	)

	WebhookProcessDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "diverge_webhook_process_duration_seconds",
			Help:    "Time to process incoming webhooks",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"provider", "action"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		EnvironmentsActive,
		PreviewGroupsActive,
		ReconcileTotal,
		ReconcileDuration,
		DeploymentDuration,
		RoutesConfigured,
		EnvironmentTTLRemaining,
		ReconcileOutcomes,
		EnvironmentTransitions,
		SubsystemErrors,
		DatabaseProvisionDuration,
		DeployDuration,
		RoutingReconcileDuration,
		WebhookProcessDuration,
	)
}
