package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	EnvironmentsActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "diverge_environments_active",
			Help: "Number of active environments by phase and provider",
		},
		[]string{"phase", "provider"},
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
