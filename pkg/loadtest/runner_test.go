package loadtest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunner_BasicLoad(t *testing.T) {
	var reqCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reqCount, 1)
		time.Sleep(2 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	runner := NewRunner()
	cfg := Config{
		TargetURL:   srv.URL,
		RoutingKey:  "preview-test-1",
		Duration:    100 * time.Millisecond,
		Concurrency: 4,
		Timeout:     time.Second,
	}

	res, err := runner.Run(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Passed)
	assert.Greater(t, res.Candidate.TotalRequests, int64(5))
	assert.Equal(t, res.Candidate.TotalRequests, res.Candidate.SuccessCount)
	assert.Equal(t, int64(0), res.Candidate.NetworkErrors)
	assert.Greater(t, res.Candidate.Latencies.P50, time.Duration(0))
	assert.Greater(t, res.Candidate.Latencies.P99, res.Candidate.Latencies.P50)
}

func TestRunner_RoutingKeyAndBaseline(t *testing.T) {
	var candidateHeadersReceived int64
	var baselineRequests int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := r.Header.Get(HeaderDivergeRoutingKey)
		if hdr == "preview-pr-42" {
			atomic.AddInt64(&candidateHeadersReceived, 1)
			time.Sleep(5 * time.Millisecond) // Candidate is slightly slower
		} else {
			atomic.AddInt64(&baselineRequests, 1)
			time.Sleep(1 * time.Millisecond)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	runner := NewRunner()
	cfg := Config{
		TargetURL:       srv.URL,
		RoutingKey:      "preview-pr-42",
		Duration:        100 * time.Millisecond,
		Concurrency:     2,
		BaselineCompare: true,
	}

	res, err := runner.Run(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.Baseline)

	assert.Greater(t, atomic.LoadInt64(&candidateHeadersReceived), int64(0))
	assert.Greater(t, atomic.LoadInt64(&baselineRequests), int64(0))
	assert.Greater(t, res.Candidate.TotalRequests, int64(0))
	assert.Greater(t, res.Baseline.TotalRequests, int64(0))
	assert.True(t, res.Passed)
}

func TestRunner_ThresholdFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	runner := NewRunner()

	// 1. MaxP99 failure
	cfgMaxP99 := Config{
		TargetURL:   srv.URL,
		Duration:    50 * time.Millisecond,
		Concurrency: 2,
		MaxP99:      1 * time.Millisecond, // Should fail since sleep is 10ms
	}
	res1, err := runner.Run(context.Background(), cfgMaxP99)
	require.NoError(t, err)
	assert.False(t, res1.Passed)
	assert.Contains(t, res1.FailureReason, "exceeded threshold")

	// 2. MaxLatencyIncreasePercent failure
	srvCompare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderDivergeRoutingKey) != "" {
			time.Sleep(20 * time.Millisecond) // Candidate 100% slower
		} else {
			time.Sleep(2 * time.Millisecond)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srvCompare.Close()

	cfgIncrease := Config{
		TargetURL:                 srvCompare.URL,
		RoutingKey:                "preview-slow",
		Duration:                  80 * time.Millisecond,
		Concurrency:               2,
		BaselineCompare:           true,
		MaxLatencyIncreasePercent: 10.0, // 10% allowed, but difference will be ~900%
	}
	res2, err := runner.Run(context.Background(), cfgIncrease)
	require.NoError(t, err)
	assert.False(t, res2.Passed)
	assert.Contains(t, res2.FailureReason, "exceeding allowed threshold")
}

func TestReport_FormatTableAndJSON(t *testing.T) {
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
		Baseline: &Result{
			TargetURL:     "http://example.com/api",
			Duration:      5 * time.Second,
			TotalRequests: 1000,
			SuccessCount:  1000,
			RPS:           200.0,
			Latencies: LatencyStats{
				Min:  time.Millisecond,
				Mean: 4 * time.Millisecond,
				P50:  3 * time.Millisecond,
				P90:  6 * time.Millisecond,
				P95:  8 * time.Millisecond,
				P99:  12 * time.Millisecond,
				Max:  20 * time.Millisecond,
			},
		},
		P99Delta:           3 * time.Millisecond,
		P99IncreasePercent: 25.0,
	}

	// Verify Table output
	var tableBuf bytes.Buffer
	err := FormatTable(&tableBuf, comp)
	require.NoError(t, err)
	out := tableBuf.String()
	assert.Contains(t, out, "DIVERGE LOAD TEST RESULTS")
	assert.Contains(t, out, "p99")
	assert.Contains(t, out, "PASSED quality thresholds")

	// Verify JSON output
	var jsonBuf bytes.Buffer
	err = FormatJSON(&jsonBuf, comp)
	require.NoError(t, err)
	var decoded ComparisonResult
	err = json.Unmarshal(jsonBuf.Bytes(), &decoded)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), decoded.Candidate.TotalRequests)
	assert.Equal(t, true, decoded.Passed)
}

func TestRunner_RPSRateLimiting(t *testing.T) {
	var count int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&count, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	runner := NewRunner()
	cfg := Config{
		TargetURL:   srv.URL,
		Duration:    150 * time.Millisecond,
		Concurrency: 2,
		RPS:         20, // 20 req/s => in 150ms should be roughly 2-5 requests
	}

	res, err := runner.Run(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Passed)
	total := atomic.LoadInt64(&count)
	assert.GreaterOrEqual(t, total, int64(1))
	assert.LessOrEqual(t, total, int64(10))
}

func TestRunner_PostWithBodyAndHeaders(t *testing.T) {
	var receivedBody []byte
	var customHeaderVal string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		customHeaderVal = r.Header.Get("X-Custom-Header")
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		receivedBody = buf.Bytes()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	runner := NewRunner()
	cfg := Config{
		TargetURL:   srv.URL,
		Method:      "POST",
		Body:        []byte(`{"action":"verify"}`),
		Headers:     map[string]string{"X-Custom-Header": "custom-val"},
		Duration:    50 * time.Millisecond,
		Concurrency: 1,
	}

	res, err := runner.Run(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Passed)
	assert.Equal(t, "custom-val", customHeaderVal)
	assert.JSONEq(t, `{"action":"verify"}`, string(receivedBody))
	assert.Equal(t, res.Candidate.TotalRequests, res.Candidate.SuccessCount)
}

func TestRunner_MaxErrorRateExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	runner := NewRunner()
	cfg := Config{
		TargetURL:    srv.URL,
		Duration:     50 * time.Millisecond,
		Concurrency:  2,
		MaxErrorRate: 0.05, // 5% max error rate
	}

	res, err := runner.Run(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.Passed)
	assert.Contains(t, res.FailureReason, "error rate")
	assert.Equal(t, res.Candidate.TotalRequests, res.Candidate.ServerErrCount)
}

func TestRunner_NetworkErrors(t *testing.T) {
	runner := NewRunner()
	// Deliberately target an unreachable port
	cfg := Config{
		TargetURL:   "http://127.0.0.1:59999",
		Duration:    50 * time.Millisecond,
		Concurrency: 1,
		Timeout:     20 * time.Millisecond,
	}

	res, err := runner.Run(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Greater(t, res.Candidate.NetworkErrors, int64(0))
	assert.NotEmpty(t, res.Candidate.ErrorSample)
}
