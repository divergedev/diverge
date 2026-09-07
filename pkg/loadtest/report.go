package loadtest

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"
)

// FormatJSON outputs the comparison result as indented JSON.
func FormatJSON(w io.Writer, comp *ComparisonResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(comp)
}

// FormatTable outputs the load test results in human-readable tabular form.
func FormatTable(w io.Writer, comp *ComparisonResult) error {
	cand := comp.Candidate

	_, _ = fmt.Fprintf(w, "\n📊 DIVERGE LOAD TEST RESULTS\n")
	_, _ = fmt.Fprintf(w, "=====================================================\n")
	_, _ = fmt.Fprintf(w, "Target URL:    %s\n", cand.TargetURL)
	if cand.RoutingKey != "" {
		_, _ = fmt.Fprintf(w, "Routing Key:   %s\n", cand.RoutingKey)
	}
	_, _ = fmt.Fprintf(w, "Test Duration: %s\n", cand.Duration.Round(time.Millisecond))
	_, _ = fmt.Fprintf(w, "Total Requests:%d (%.1f req/s)\n", cand.TotalRequests, cand.RPS)

	// Status breakdown
	_, _ = fmt.Fprintf(w, "\nStatus Breakdown:\n")
	_, _ = fmt.Fprintf(w, "  2xx Success:  %d\n", cand.SuccessCount)
	if cand.RedirectCount > 0 {
		_, _ = fmt.Fprintf(w, "  3xx Redirect: %d\n", cand.RedirectCount)
	}
	if cand.ClientErrCount > 0 {
		_, _ = fmt.Fprintf(w, "  4xx Client:   %d\n", cand.ClientErrCount)
	}
	if cand.ServerErrCount > 0 {
		_, _ = fmt.Fprintf(w, "  5xx Server:   %d\n", cand.ServerErrCount)
	}
	if cand.NetworkErrors > 0 {
		_, _ = fmt.Fprintf(w, "  Network Errs: %d\n", cand.NetworkErrors)
	}

	// Latency Table
	_, _ = fmt.Fprintf(w, "\nLatency Percentiles:\n")
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	if comp.Baseline != nil {
		base := comp.Baseline
		_, _ = fmt.Fprintf(tw, "  Metric\tBaseline\tCandidate\tDelta\n")
		_, _ = fmt.Fprintf(tw, "  ------\t--------\t---------\t-----\n")
		_, _ = fmt.Fprintf(tw, "  Min\t%s\t%s\t%s\n", fmtDur(base.Latencies.Min), fmtDur(cand.Latencies.Min), fmtDeltaDur(cand.Latencies.Min-base.Latencies.Min))
		_, _ = fmt.Fprintf(tw, "  Mean\t%s\t%s\t%s\n", fmtDur(base.Latencies.Mean), fmtDur(cand.Latencies.Mean), fmtDeltaDur(cand.Latencies.Mean-base.Latencies.Mean))
		_, _ = fmt.Fprintf(tw, "  p50\t%s\t%s\t%s\n", fmtDur(base.Latencies.P50), fmtDur(cand.Latencies.P50), fmtDeltaDur(cand.Latencies.P50-base.Latencies.P50))
		_, _ = fmt.Fprintf(tw, "  p90\t%s\t%s\t%s\n", fmtDur(base.Latencies.P90), fmtDur(cand.Latencies.P90), fmtDeltaDur(cand.Latencies.P90-base.Latencies.P90))
		_, _ = fmt.Fprintf(tw, "  p95\t%s\t%s\t%s\n", fmtDur(base.Latencies.P95), fmtDur(cand.Latencies.P95), fmtDeltaDur(cand.Latencies.P95-base.Latencies.P95))
		_, _ = fmt.Fprintf(tw, "  p99\t%s\t%s\t%s (%.1f%%)\n", fmtDur(base.Latencies.P99), fmtDur(cand.Latencies.P99), fmtDeltaDur(comp.P99Delta), comp.P99IncreasePercent)
		_, _ = fmt.Fprintf(tw, "  Max\t%s\t%s\t%s\n", fmtDur(base.Latencies.Max), fmtDur(cand.Latencies.Max), fmtDeltaDur(cand.Latencies.Max-base.Latencies.Max))
	} else {
		_, _ = fmt.Fprintf(tw, "  Metric\tCandidate\n")
		_, _ = fmt.Fprintf(tw, "  ------\t---------\n")
		_, _ = fmt.Fprintf(tw, "  Min\t%s\n", fmtDur(cand.Latencies.Min))
		_, _ = fmt.Fprintf(tw, "  Mean\t%s\n", fmtDur(cand.Latencies.Mean))
		_, _ = fmt.Fprintf(tw, "  p50\t%s\n", fmtDur(cand.Latencies.P50))
		_, _ = fmt.Fprintf(tw, "  p90\t%s\n", fmtDur(cand.Latencies.P90))
		_, _ = fmt.Fprintf(tw, "  p95\t%s\n", fmtDur(cand.Latencies.P95))
		_, _ = fmt.Fprintf(tw, "  p99\t%s\n", fmtDur(cand.Latencies.P99))
		_, _ = fmt.Fprintf(tw, "  Max\t%s\n", fmtDur(cand.Latencies.Max))
	}
	_ = tw.Flush()

	// Overall outcome
	_, _ = fmt.Fprintf(w, "\n=====================================================\n")
	if comp.Passed {
		_, _ = fmt.Fprintf(w, "✅ Outcome: PASSED quality thresholds\n")
	} else {
		_, _ = fmt.Fprintf(w, "❌ Outcome: FAILED - %s\n", comp.FailureReason)
	}
	_, _ = fmt.Fprintf(w, "=====================================================\n\n")

	return nil
}

func fmtDur(d time.Duration) string {
	if d == 0 {
		return "0ms"
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%.2fµs", float64(d.Nanoseconds())/1000.0)
	}
	return fmt.Sprintf("%.2fms", float64(d.Nanoseconds())/1000000.0)
}

func fmtDeltaDur(d time.Duration) string {
	if d > 0 {
		return "+" + fmtDur(d)
	}
	return fmtDur(d)
}
