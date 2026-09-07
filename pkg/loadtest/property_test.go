package loadtest

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

// TestProperty_PercentileOrdering verifies that for any distribution of latencies,
// the statistical ordering holds: Min <= P50 <= P90 <= P95 <= P99 <= Max.
func TestProperty_PercentileOrdering(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 200).Draw(t, "numSamples")
		collector := NewMetricsCollector("http://example.com", "test-key")
		collector.Start()

		var generated []time.Duration
		for i := 0; i < n; i++ {
			micros := rapid.Int64Range(1, 1000000).Draw(t, "durationMicros")
			d := time.Duration(micros) * time.Microsecond
			generated = append(generated, d)
			collector.Record(RequestRecord{
				Duration:   d,
				StatusCode: 200,
			})
		}

		res := collector.Finalize()
		assert.Equal(t, int64(n), res.TotalRequests)
		assert.True(t, res.Latencies.Min <= res.Latencies.P50, "Min <= P50 failed")
		assert.True(t, res.Latencies.P50 <= res.Latencies.P90, "P50 <= P90 failed")
		assert.True(t, res.Latencies.P90 <= res.Latencies.P95, "P90 <= P95 failed")
		assert.True(t, res.Latencies.P95 <= res.Latencies.P99, "P95 <= P99 failed")
		assert.True(t, res.Latencies.P99 <= res.Latencies.Max, "P99 <= Max failed")
	})
}

// TestProperty_StatusCodeSum verifies that across random combinations of 2xx, 3xx, 4xx, 5xx, and network errors,
// the total request count exactly equals the sum of categorized counts.
func TestProperty_StatusCodeSum(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		recordsCount := rapid.IntRange(1, 100).Draw(t, "recordsCount")
		collector := NewMetricsCollector("http://example.com", "")
		collector.Start()

		for i := 0; i < recordsCount; i++ {
			isNetErr := rapid.Bool().Draw(t, "isNetErr")
			if isNetErr {
				collector.Record(RequestRecord{
					Duration: time.Millisecond,
					Error:    assert.AnError,
				})
			} else {
				statusCode := rapid.SampledFrom([]int{
					200, 201, 204,
					301, 302, 307,
					400, 401, 403, 404, 429,
					500, 502, 503, 504,
				}).Draw(t, "statusCode")
				collector.Record(RequestRecord{
					Duration:   time.Millisecond,
					StatusCode: statusCode,
				})
			}
		}

		res := collector.Finalize()
		assert.Equal(t, int64(recordsCount), res.TotalRequests)
		categorized := res.SuccessCount + res.RedirectCount + res.ClientErrCount + res.ServerErrCount + res.NetworkErrors
		assert.Equal(t, res.TotalRequests, categorized, "Sum of categorized counts must equal total requests")
		assert.False(t, math.IsNaN(res.RPS))
	})
}
