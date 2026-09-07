package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/divergedev/diverge/pkg/loadtest"
)

func newLoadtestCmd(app *App) *cobra.Command {
	var (
		preview                   string
		routingKey                string
		duration                  time.Duration
		rps                       int
		concurrency               int
		timeout                   time.Duration
		baseline                  bool
		maxP99                    time.Duration
		maxLatencyIncreasePercent float64
		outputJSON                bool
		method                    string
	)

	cmd := &cobra.Command{
		Use:   "loadtest <url>",
		Short: "Run targeted ephemeral load and latency benchmark tests",
		Long: `Run concurrent HTTP load and latency benchmark tests against preview environments.
Supports header-based routing verification (x-diverge-routing-key), side-by-side
baseline comparison, and automated CI/CD quality gate thresholds.`,
		Example: `	# Test preview endpoint for 10s at 50 RPS
	diverge loadtest https://api.preview.example.com --preview pr-42 --rps 50

	# Compare candidate preview against baseline production/staging
	diverge loadtest https://api.example.com --routing-key pr-42 --baseline

	# CI Quality gate: fail if p99 latency exceeds 150ms or is >20% slower than baseline
	diverge loadtest https://api.example.com --routing-key pr-42 --baseline --max-p99 150ms --fail-on-latency-increase 20`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetURL := args[0]

			effectiveRoutingKey := routingKey
			if effectiveRoutingKey == "" && preview != "" {
				effectiveRoutingKey = preview
			}

			cfg := loadtest.Config{
				TargetURL:                 targetURL,
				RoutingKey:                effectiveRoutingKey,
				Duration:                  duration,
				RPS:                       rps,
				Concurrency:               concurrency,
				Timeout:                   timeout,
				Method:                    method,
				BaselineCompare:           baseline,
				MaxP99:                    maxP99,
				MaxLatencyIncreasePercent: maxLatencyIncreasePercent,
			}

			runner := loadtest.NewRunner()
			res, err := runner.Run(cmd.Context(), cfg)
			if err != nil {
				return fmt.Errorf("load test failed: %w", err)
			}

			if outputJSON {
				if err := loadtest.FormatJSON(cmd.OutOrStdout(), res); err != nil {
					return err
				}
			} else {
				if err := loadtest.FormatTable(cmd.OutOrStdout(), res); err != nil {
					return err
				}
			}

			if !res.Passed {
				return fmt.Errorf("quality gate threshold failed: %s", res.FailureReason)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&preview, "preview", "p", "", "Preview environment name")
	cmd.Flags().StringVarP(&routingKey, "routing-key", "k", "", "Diverge routing key (x-diverge-routing-key header)")
	cmd.Flags().DurationVarP(&duration, "duration", "d", 10*time.Second, "Test duration (e.g. 5s, 30s, 1m)")
	cmd.Flags().IntVar(&rps, "rps", 0, "Target requests per second (0 = unthrottled)")
	cmd.Flags().IntVarP(&concurrency, "concurrency", "c", 10, "Concurrent worker connections")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "Per-request timeout")
	cmd.Flags().BoolVar(&baseline, "baseline", false, "Compare candidate with baseline (no routing header)")
	cmd.Flags().DurationVar(&maxP99, "max-p99", 0, "Fail if candidate p99 exceeds threshold")
	cmd.Flags().Float64Var(&maxLatencyIncreasePercent, "fail-on-latency-increase", 0, "Fail if candidate p99 is >X% slower than baseline")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Output results in JSON format")
	cmd.Flags().StringVarP(&method, "method", "X", "GET", "HTTP method")

	return cmd
}
