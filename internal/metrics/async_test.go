package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestAsyncMetrics(t *testing.T) {
	AsyncProvisionErrorsTotal.WithLabelValues("kafka").Inc()
	assert.Equal(t, float64(1), testutil.ToFloat64(AsyncProvisionErrorsTotal.WithLabelValues("kafka")))

	AsyncRoutesActive.WithLabelValues("kafka", "default").Set(2)
	assert.Equal(t, float64(2), testutil.ToFloat64(AsyncRoutesActive.WithLabelValues("kafka", "default")))

	AsyncProvisionDurationSeconds.WithLabelValues("kafka", "success").Observe(1.5)
}
