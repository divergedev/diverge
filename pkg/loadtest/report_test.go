package loadtest

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReport_FormatTableAndJSONDetailed(t *testing.T) {
	comp := &ComparisonResult{
		Passed: true,
		Candidate: &Result{
			TargetURL:      "http://example.com/api",
			RoutingKey:     "test-env",
			Duration:       5 * time.Second,
			TotalRequests:  1000,
			SuccessCount:   990,
			ServerErrCount: 10,
			RPS:            200.0,
			Latencies: LatencyStats{
				Min:  time.Millisecond,
				Mean: 5 * time.Millisecond,
				P50:  4 * time.Millisecond,
				P90:  8 * time.Millisecond,
				P95:  10 * time.Millisecond,
				P99:  15 * time.Millisecond,
				Max:  30 * time.Millisecond,
			},
		},
	}

	var tableBuf bytes.Buffer
	err := FormatTable(&tableBuf, comp)
	require.NoError(t, err)
	assert.Contains(t, tableBuf.String(), "DIVERGE LOAD TEST RESULTS")

	var jsonBuf bytes.Buffer
	err = FormatJSON(&jsonBuf, comp)
	require.NoError(t, err)

	var decoded ComparisonResult
	err = json.Unmarshal(jsonBuf.Bytes(), &decoded)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), decoded.Candidate.TotalRequests)
}
