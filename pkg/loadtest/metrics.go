package loadtest

import (
	"sort"
	"sync"
	"time"
)

// RequestRecord captures the result of a single load test request.
type RequestRecord struct {
	Duration   time.Duration
	StatusCode int
	Error      error
}

// Result summarizes the execution metrics of a load test run.
type Result struct {
	TargetURL      string        `json:"target_url"`
	RoutingKey     string        `json:"routing_key,omitempty"`
	Duration       time.Duration `json:"duration"`
	TotalRequests  int64         `json:"total_requests"`
	SuccessCount   int64         `json:"success_count"`    // 2xx
	RedirectCount  int64         `json:"redirect_count"`   // 3xx
	ClientErrCount int64         `json:"client_err_count"` // 4xx
	ServerErrCount int64         `json:"server_err_count"` // 5xx
	NetworkErrors  int64         `json:"network_errors"`
	RPS            float64       `json:"rps"`
	StatusCodes    map[int]int64 `json:"status_codes"`
	Latencies      LatencyStats  `json:"latencies"`
	ErrorSample    []string      `json:"error_sample,omitempty"`
}

// LatencyStats contains statistical latency percentiles.
type LatencyStats struct {
	Min  time.Duration `json:"min"`
	Mean time.Duration `json:"mean"`
	P50  time.Duration `json:"p50"`
	P90  time.Duration `json:"p90"`
	P95  time.Duration `json:"p95"`
	P99  time.Duration `json:"p99"`
	Max  time.Duration `json:"max"`
}

// MetricsCollector accumulates request measurements thread-safely.
type MetricsCollector struct {
	mu          sync.Mutex
	targetURL   string
	routingKey  string
	startTime   time.Time
	endTime     time.Time
	statusCodes map[int]int64
	latencies   []time.Duration
	networkErrs int64
	errorSample []string
}

// NewMetricsCollector initializes a fresh collector.
func NewMetricsCollector(targetURL, routingKey string) *MetricsCollector {
	return &MetricsCollector{
		targetURL:   targetURL,
		routingKey:  routingKey,
		statusCodes: make(map[int]int64),
		latencies:   make([]time.Duration, 0, 1024),
		errorSample: make([]string, 0, 10),
	}
}

// Start marks the beginning of the test.
func (c *MetricsCollector) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.startTime = time.Now()
}

// Record records a batch of request measurements.
func (c *MetricsCollector) Record(rec RequestRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.latencies = append(c.latencies, rec.Duration)
	if rec.Error != nil {
		c.networkErrs++
		if len(c.errorSample) < 10 {
			c.errorSample = append(c.errorSample, rec.Error.Error())
		}
	} else {
		c.statusCodes[rec.StatusCode]++
	}
}

// Finalize calculates summary statistics and percentiles.
func (c *MetricsCollector) Finalize() *Result {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.endTime.IsZero() {
		c.endTime = time.Now()
	}

	totalTime := c.endTime.Sub(c.startTime)
	if totalTime <= 0 {
		totalTime = time.Millisecond
	}

	res := &Result{
		TargetURL:     c.targetURL,
		RoutingKey:    c.routingKey,
		Duration:      totalTime,
		TotalRequests: int64(len(c.latencies)),
		NetworkErrors: c.networkErrs,
		StatusCodes:   make(map[int]int64),
		ErrorSample:   c.errorSample,
	}

	for code, count := range c.statusCodes {
		res.StatusCodes[code] = count
		switch {
		case code >= 200 && code < 300:
			res.SuccessCount += count
		case code >= 300 && code < 400:
			res.RedirectCount += count
		case code >= 400 && code < 500:
			res.ClientErrCount += count
		case code >= 500 && code < 600:
			res.ServerErrCount += count
		}
	}

	res.RPS = float64(res.TotalRequests) / totalTime.Seconds()

	if len(c.latencies) == 0 {
		return res
	}

	// Calculate latency statistics
	sorted := make([]time.Duration, len(c.latencies))
	copy(sorted, c.latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var sum time.Duration
	for _, l := range sorted {
		sum += l
	}

	res.Latencies.Min = sorted[0]
	res.Latencies.Max = sorted[len(sorted)-1]
	res.Latencies.Mean = time.Duration(int64(sum) / int64(len(sorted)))
	res.Latencies.P50 = percentile(sorted, 50)
	res.Latencies.P90 = percentile(sorted, 90)
	res.Latencies.P95 = percentile(sorted, 95)
	res.Latencies.P99 = percentile(sorted, 99)

	return res
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)) * (p / 100.0))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
