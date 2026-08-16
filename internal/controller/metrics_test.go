package controller

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestAsyncMetrics(t *testing.T) {
	// Reset metrics before testing to ensure clean state
	asyncProvisionsTotal.Reset()
	asyncProvisionDuration.Reset()
	asyncTeardownsTotal.Reset()
	asyncActiveRoutes.Reset()

	// Test asyncProvisionsTotal
	asyncProvisionsTotal.WithLabelValues("kafka", "success").Inc()
	assert.Equal(t, float64(1), testutil.ToFloat64(asyncProvisionsTotal.WithLabelValues("kafka", "success")))

	asyncProvisionsTotal.WithLabelValues("temporal", "error").Inc()
	assert.Equal(t, float64(1), testutil.ToFloat64(asyncProvisionsTotal.WithLabelValues("temporal", "error")))

	// Test asyncProvisionDuration
	asyncProvisionDuration.WithLabelValues("kafka").Observe(1.5)

	// Test asyncTeardownsTotal
	asyncTeardownsTotal.WithLabelValues("kafka", "error").Inc()
	assert.Equal(t, float64(1), testutil.ToFloat64(asyncTeardownsTotal.WithLabelValues("kafka", "error")))

	asyncTeardownsTotal.WithLabelValues("temporal", "success").Inc()
	assert.Equal(t, float64(1), testutil.ToFloat64(asyncTeardownsTotal.WithLabelValues("temporal", "success")))

	// Test asyncActiveRoutes
	asyncActiveRoutes.WithLabelValues("temporal").Set(2)
	assert.Equal(t, float64(2), testutil.ToFloat64(asyncActiveRoutes.WithLabelValues("temporal")))
}
