package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestMetricsRegistrationAndUsage(t *testing.T) {
	// Test Counter
	ReconcileOutcomes.WithLabelValues("success").Inc()
	assert.Equal(t, float64(1), testutil.ToFloat64(ReconcileOutcomes.WithLabelValues("success")))

	// Test Gauge
	EnvironmentsActive.WithLabelValues("running", "aws").Set(5)
	assert.Equal(t, float64(5), testutil.ToFloat64(EnvironmentsActive.WithLabelValues("running", "aws")))

	EnvironmentTTLRemaining.WithLabelValues("env-1", "default").Set(3600)
	assert.Equal(t, float64(3600), testutil.ToFloat64(EnvironmentTTLRemaining.WithLabelValues("env-1", "default")))

	EnvironmentTransitions.WithLabelValues("pending", "running", "aws").Inc()
	assert.Equal(t, float64(1), testutil.ToFloat64(EnvironmentTransitions.WithLabelValues("pending", "running", "aws")))

	SubsystemErrors.WithLabelValues("db", "create").Inc()
	assert.Equal(t, float64(1), testutil.ToFloat64(SubsystemErrors.WithLabelValues("db", "create")))

	// Test Histograms (no easy ToFloat64 for Histograms in testutil directly that doesn't need metric type assertions, but observing ensures no panics)
	DatabaseProvisionDuration.WithLabelValues("postgres").Observe(1.5)
	DeployDuration.WithLabelValues("helm").Observe(2.0)
	RoutingReconcileDuration.WithLabelValues("ingress").Observe(0.5)
	WebhookProcessDuration.WithLabelValues("github", "push").Observe(0.2)
}
