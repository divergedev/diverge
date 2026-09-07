package loadtest

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMetricsCollector_Calculations(t *testing.T) {
	c := NewMetricsCollector("http://example.com", "test-key")
	c.Start()

	// Empty finalize
	emptyRes := c.Finalize()
	assert.Equal(t, int64(0), emptyRes.TotalRequests)
	assert.Equal(t, time.Duration(0), emptyRes.Latencies.P50)

	// Record samples
	c.Record(RequestRecord{Duration: 10 * time.Millisecond, StatusCode: 200})
	c.Record(RequestRecord{Duration: 20 * time.Millisecond, StatusCode: 200})
	c.Record(RequestRecord{Duration: 30 * time.Millisecond, StatusCode: 500})
	c.Record(RequestRecord{Duration: 15 * time.Millisecond, Error: errors.New("timeout")})

	res := c.Finalize()
	assert.Equal(t, int64(4), res.TotalRequests)
	assert.Equal(t, int64(2), res.SuccessCount)
	assert.Equal(t, int64(1), res.ServerErrCount)
	assert.Equal(t, int64(1), res.NetworkErrors)
	assert.Len(t, res.ErrorSample, 1)

	assert.Equal(t, 10*time.Millisecond, res.Latencies.Min)
	assert.Equal(t, 30*time.Millisecond, res.Latencies.Max)
	assert.Equal(t, 20*time.Millisecond, res.Latencies.P50)
}
