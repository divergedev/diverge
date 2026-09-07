package loadtest

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const (
	HeaderDivergeRoutingKey = "x-diverge-routing-key"
	DefaultConcurrency      = 10
	DefaultTimeout          = 5 * time.Second
	DefaultDuration         = 10 * time.Second
)

// Config configures a load test execution.
type Config struct {
	TargetURL                 string            `json:"target_url"`
	RoutingKey                string            `json:"routing_key"`
	Duration                  time.Duration     `json:"duration"`
	RPS                       int               `json:"rps"` // 0 = unthrottled
	Concurrency               int               `json:"concurrency"`
	Timeout                   time.Duration     `json:"timeout"`
	Method                    string            `json:"method"`
	Headers                   map[string]string `json:"headers"`
	Body                      []byte            `json:"body,omitempty"`
	BaselineCompare           bool              `json:"baseline_compare"`
	MaxP99                    time.Duration     `json:"max_p99,omitempty"`
	MaxLatencyIncreasePercent float64           `json:"max_latency_increase_percent,omitempty"`
}

// ComparisonResult represents the outcome of a load test, optionally comparing
// candidate preview results against baseline production/staging results.
type ComparisonResult struct {
	Candidate           *Result       `json:"candidate"`
	Baseline            *Result       `json:"baseline,omitempty"`
	P99Delta            time.Duration `json:"p99_delta,omitempty"`
	P99IncreasePercent  float64       `json:"p99_increase_percent,omitempty"`
	MeanDelta           time.Duration `json:"mean_delta,omitempty"`
	MeanIncreasePercent float64       `json:"mean_increase_percent,omitempty"`
	Passed              bool          `json:"passed"`
	FailureReason       string        `json:"failure_reason,omitempty"`
}

// Runner executes concurrent HTTP load tests.
type Runner struct {
	client *http.Client
}

// NewRunner creates a new Runner with an optimized HTTP transport.
func NewRunner() *Runner {
	transport := &http.Transport{
		MaxIdleConns:        1000,
		MaxIdleConnsPerHost: 200,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // cluster preview endpoints may use self-signed certs
		},
		DisableCompression: false,
	}

	return &Runner{
		client: &http.Client{
			Transport: transport,
			Timeout:   DefaultTimeout,
		},
	}
}

// Run executes the load test according to the supplied configuration.
func (r *Runner) Run(ctx context.Context, cfg Config) (*ComparisonResult, error) {
	if cfg.TargetURL == "" {
		return nil, fmt.Errorf("target URL is required")
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = DefaultConcurrency
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Duration <= 0 {
		cfg.Duration = DefaultDuration
	}
	if cfg.Method == "" {
		cfg.Method = http.MethodGet
	}

	r.client.Timeout = cfg.Timeout

	// 1. Run Candidate (with routing key if present)
	candidateResult, err := r.runSingle(ctx, cfg, cfg.RoutingKey)
	if err != nil {
		return nil, fmt.Errorf("candidate load test failed: %w", err)
	}

	comp := &ComparisonResult{
		Candidate: candidateResult,
		Passed:    true,
	}

	// 2. If Baseline comparison requested, run without routing key
	if cfg.BaselineCompare {
		baselineResult, err := r.runSingle(ctx, cfg, "")
		if err != nil {
			return nil, fmt.Errorf("baseline load test failed: %w", err)
		}
		comp.Baseline = baselineResult

		// Calculate differentials
		comp.P99Delta = candidateResult.Latencies.P99 - baselineResult.Latencies.P99
		if baselineResult.Latencies.P99 > 0 {
			comp.P99IncreasePercent = float64(comp.P99Delta) / float64(baselineResult.Latencies.P99) * 100.0
		}

		comp.MeanDelta = candidateResult.Latencies.Mean - baselineResult.Latencies.Mean
		if baselineResult.Latencies.Mean > 0 {
			comp.MeanIncreasePercent = float64(comp.MeanDelta) / float64(baselineResult.Latencies.Mean) * 100.0
		}
	}

	// 3. Evaluate Thresholds
	if cfg.MaxP99 > 0 && candidateResult.Latencies.P99 > cfg.MaxP99 {
		comp.Passed = false
		comp.FailureReason = fmt.Sprintf("p99 latency %s exceeded threshold %s",
			candidateResult.Latencies.P99.Round(time.Millisecond),
			cfg.MaxP99.Round(time.Millisecond))
	} else if cfg.BaselineCompare && cfg.MaxLatencyIncreasePercent > 0 && comp.P99IncreasePercent > cfg.MaxLatencyIncreasePercent {
		comp.Passed = false
		comp.FailureReason = fmt.Sprintf("p99 latency increased by %.1f%%, exceeding allowed threshold of %.1f%%",
			comp.P99IncreasePercent,
			cfg.MaxLatencyIncreasePercent)
	}

	return comp, nil
}

func (r *Runner) runSingle(ctx context.Context, cfg Config, routingKey string) (*Result, error) {
	collector := NewMetricsCollector(cfg.TargetURL, routingKey)
	collector.Start()

	testCtx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()

	var limiter <-chan time.Time
	if cfg.RPS > 0 {
		interval := time.Second / time.Duration(cfg.RPS)
		if interval <= 0 {
			interval = time.Microsecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		limiter = ticker.C
	}

	var wg sync.WaitGroup
	var activeWorkers int64

	workChan := make(chan struct{}, cfg.Concurrency*2)

	// Worker pool
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-testCtx.Done():
					return
				case _, ok := <-workChan:
					if !ok {
						return
					}
					rec := r.doRequest(testCtx, cfg, routingKey)
					collector.Record(rec)
				}
			}
		}()
	}

	// Dispatch loop
dispatchLoop:
	for {
		select {
		case <-testCtx.Done():
			break dispatchLoop
		default:
			if limiter != nil {
				select {
				case <-testCtx.Done():
					break dispatchLoop
				case <-limiter:
				}
			}

			select {
			case workChan <- struct{}{}:
				atomic.AddInt64(&activeWorkers, 1)
			case <-testCtx.Done():
				break dispatchLoop
			}
		}
	}

	close(workChan)
	wg.Wait()

	return collector.Finalize(), nil
}

func (r *Runner) doRequest(parentCtx context.Context, cfg Config, routingKey string) RequestRecord {
	reqCtx, reqCancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer reqCancel()

	var bodyReader *bytes.Reader
	if len(cfg.Body) > 0 {
		bodyReader = bytes.NewReader(cfg.Body)
	}

	var req *http.Request
	var err error
	if bodyReader != nil {
		req, err = http.NewRequestWithContext(reqCtx, cfg.Method, cfg.TargetURL, bodyReader)
	} else {
		req, err = http.NewRequestWithContext(reqCtx, cfg.Method, cfg.TargetURL, nil)
	}

	if err != nil {
		return RequestRecord{Error: err}
	}

	// Apply custom headers
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	// Inject Diverge Routing Key if present
	if routingKey != "" {
		req.Header.Set(HeaderDivergeRoutingKey, routingKey)
	}

	start := time.Now()
	resp, err := r.client.Do(req)
	duration := time.Since(start)

	if err != nil {
		return RequestRecord{
			Duration: duration,
			Error:    err,
		}
	}
	defer resp.Body.Close()

	return RequestRecord{
		Duration:   duration,
		StatusCode: resp.StatusCode,
	}
}
