package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/protocgen/proto2mcp/pkg/mcpruntime"

	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	divergev1alpha1connect "github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/pkg/doctor"
	"github.com/divergedev/diverge/pkg/loadtest"
)

var waitForReadySchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "Name of the environment to wait for"},
		"namespace": {"type": "string", "description": "Kubernetes namespace of the environment"}
	},
	"required": ["name", "namespace"]
}`)

var fetchErrorsSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "Name of the environment to fetch errors from"},
		"namespace": {"type": "string", "description": "Kubernetes namespace of the environment"},
		"lines": {"type": "integer", "description": "Maximum number of error lines to return (default: 50)"}
	},
	"required": ["name", "namespace"]
}`)

var loadtestSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"target_url": {"type": "string", "description": "Target endpoint URL to benchmark"},
		"routing_key": {"type": "string", "description": "Diverge routing key header (x-diverge-routing-key)"},
		"duration_seconds": {"type": "integer", "description": "Duration in seconds (default: 5)"},
		"rps": {"type": "integer", "description": "Requests per second (0 for unthrottled)"},
		"concurrency": {"type": "integer", "description": "Concurrent worker count (default: 5)"},
		"baseline_compare": {"type": "boolean", "description": "Whether to also benchmark baseline without routing header"}
	},
	"required": ["target_url"]
}`)

func registerWaitForReady(registry mcpruntime.Registry, client divergev1alpha1connect.EnvironmentServiceClient) {
	registry.Register(mcpruntime.ToolDefinition{
		Name:        "diverge_wait_for_ready",
		Description: "Block until a preview environment reaches Ready or Failed phase. Use this after creating an environment instead of polling status repeatedly. Returns the final environment state. Maximum wait: 5 minutes.",
		InputSchema: waitForReadySchema,
	}, func(ctx context.Context, req mcpruntime.ToolRequest) (*mcpruntime.CallToolResult, error) {
		var params struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal(req.Arguments, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		timeout := time.After(5 * time.Minute)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		checkStatus := func() (*mcpruntime.CallToolResult, bool) {
			resp, err := client.GetEnvironment(ctx, connect.NewRequest(&divergev1alpha1.GetEnvironmentRequest{
				Name:      params.Name,
				Namespace: params.Namespace,
			}))
			if err != nil {
				return nil, false // Retry on transient errors
			}

			env := resp.Msg.Environment
			if env == nil {
				return nil, false
			}

			phase := env.Status.Phase
			if phase == "Running" || phase == "Ready" || phase == "Failed" || phase == "Error" {
				result, _ := json.Marshal(map[string]interface{}{
					"name":      env.Name,
					"namespace": env.Namespace,
					"phase":     phase,
				})
				isErr := phase == "Failed" || phase == "Error"
				return &mcpruntime.CallToolResult{
					Content: json.RawMessage(result),
					IsError: isErr,
				}, true
			}
			return nil, false
		}

		if res, ok := checkStatus(); ok {
			return res, nil
		}

		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timeout:
				return &mcpruntime.CallToolResult{
					Content: json.RawMessage(`{"error": "timeout waiting for environment to become ready"}`),
					IsError: true,
				}, nil
			case <-ticker.C:
				if res, ok := checkStatus(); ok {
					return res, nil
				}
			}
		}
	})
}

func registerFetchErrors(registry mcpruntime.Registry, client divergev1alpha1connect.EnvironmentServiceClient) {
	registry.Register(mcpruntime.ToolDefinition{
		Name:        "diverge_fetch_errors",
		Description: "Fetch the last error-level log lines from a preview environment. Use this to debug deployment failures instead of streaming all logs. Returns only ERROR and FATAL level entries, truncated to protect context windows.",
		InputSchema: fetchErrorsSchema,
	}, func(ctx context.Context, req mcpruntime.ToolRequest) (*mcpruntime.CallToolResult, error) {
		var params struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
			Lines     int    `json:"lines"`
		}
		if err := json.Unmarshal(req.Arguments, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if params.Lines <= 0 {
			params.Lines = 50
		}

		logCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		stream, err := client.StreamLogs(logCtx, connect.NewRequest(&divergev1alpha1.StreamLogsRequest{
			EnvironmentName: params.Name,
			Namespace:       params.Namespace,
		}))
		if err != nil {
			return nil, fmt.Errorf("failed to stream logs: %w", err)
		}

		var errorLines []string
		for stream.Receive() {
			msg := stream.Msg()
			line := msg.Content // content instead of line, checking streamlogsresponse
			if containsErrorLevel(line) {
				errorLines = append(errorLines, line)
				if len(errorLines) > params.Lines {
					errorLines = errorLines[1:]
				}
			}
		}

		result, _ := json.Marshal(map[string]interface{}{
			"environment": params.Name,
			"namespace":   params.Namespace,
			"error_count": len(errorLines),
			"lines":       errorLines,
		})

		return &mcpruntime.CallToolResult{
			Content: json.RawMessage(result),
		}, nil
	})
}

func containsErrorLevel(line string) bool {
	lowerLine := strings.ToLower(line)
	for _, indicator := range []string{"error", "fatal", "panic", "level=error", "level=fatal"} {
		if strings.Contains(lowerLine, indicator) {
			return true
		}
	}
	return false
}

// validateTargetURL validates that targetURL has http/https scheme, a non-empty host,
// and protects against SSRF (disallowing cloud metadata addresses, link-local unicast/multicast,
// and enforcing DIVERGE_ALLOWED_HOSTS allowlist if configured).
func validateTargetURL(targetURL string) error {
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("invalid target URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("target_url must be a valid http or https URL")
	}
	hostname := parsedURL.Hostname()
	if hostname == "" {
		return fmt.Errorf("target_url host cannot be empty")
	}

	lowerHost := strings.ToLower(hostname)
	if lowerHost == "metadata.google.internal" || lowerHost == "metadata" || lowerHost == "instance-data" {
		return fmt.Errorf("target_url destination %q is prohibited", hostname)
	}

	if ip := net.ParseIP(hostname); ip != nil {
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.Equal(net.ParseIP("169.254.169.254")) {
			return fmt.Errorf("target_url destination IP %s is prohibited", hostname)
		}
	}

	if allowed := os.Getenv("DIVERGE_ALLOWED_HOSTS"); allowed != "" {
		matched := false
		for _, pattern := range strings.Split(allowed, ",") {
			pattern = strings.TrimSpace(strings.ToLower(pattern))
			if pattern == "" {
				continue
			}
			if pattern == "*" || pattern == lowerHost || strings.HasSuffix(lowerHost, "."+pattern) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("target_url host %q is not in the allowed hosts list", hostname)
		}
	}
	return nil
}

func registerLoadtest(registry mcpruntime.Registry) {
	registry.Register(mcpruntime.ToolDefinition{
		Name:        "diverge_loadtest",
		Description: "Run a targeted ephemeral load and benchmark test against a preview environment or endpoint. Measures latency percentiles (p50, p90, p95, p99) and error rates, with optional baseline comparison.",
		InputSchema: loadtestSchema,
	}, func(ctx context.Context, req mcpruntime.ToolRequest) (*mcpruntime.CallToolResult, error) {
		var params struct {
			TargetURL       string `json:"target_url"`
			RoutingKey      string `json:"routing_key"`
			DurationSeconds int    `json:"duration_seconds"`
			RPS             int    `json:"rps"`
			Concurrency     int    `json:"concurrency"`
			BaselineCompare bool   `json:"baseline_compare"`
		}
		if err := json.Unmarshal(req.Arguments, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if err := validateTargetURL(params.TargetURL); err != nil {
			return &mcpruntime.CallToolResult{
				Content: json.RawMessage(fmt.Sprintf(`{"error": %q}`, err.Error())),
				IsError: true,
			}, nil
		}

		duration := time.Duration(params.DurationSeconds) * time.Second
		if duration <= 0 {
			duration = 5 * time.Second
		}
		concurrency := params.Concurrency
		if concurrency <= 0 {
			concurrency = 5
		}

		cfg := loadtest.Config{
			TargetURL:       params.TargetURL,
			RoutingKey:      params.RoutingKey,
			Duration:        duration,
			RPS:             params.RPS,
			Concurrency:     concurrency,
			BaselineCompare: params.BaselineCompare,
		}

		runner := loadtest.NewRunner()
		res, err := runner.Run(ctx, cfg)
		if err != nil {
			return &mcpruntime.CallToolResult{
				Content: json.RawMessage(fmt.Sprintf(`{"error": %q}`, err.Error())),
				IsError: true,
			}, nil
		}

		data, err := json.Marshal(res)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal load test result: %w", err)
		}

		return &mcpruntime.CallToolResult{
			Content: json.RawMessage(data),
			IsError: !res.Passed,
		}, nil
	})
}

var doctorSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "Name of the environment to diagnose"},
		"namespace": {"type": "string", "description": "Kubernetes namespace of the environment"}
	},
	"required": ["name", "namespace"]
}`)

func registerDoctor(registry mcpruntime.Registry, client divergev1alpha1connect.EnvironmentServiceClient, diagnoser ...*doctor.Diagnoser) {
	var diag *doctor.Diagnoser
	if len(diagnoser) > 0 {
		diag = diagnoser[0]
	}

	registry.Register(mcpruntime.ToolDefinition{
		Name:        "diverge_doctor",
		Description: "Diagnose an environment or workload failure. Evaluates phase, status conditions, and provides root-cause recommendations.",
		InputSchema: doctorSchema,
	}, func(ctx context.Context, req mcpruntime.ToolRequest) (*mcpruntime.CallToolResult, error) {
		var params struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal(req.Arguments, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		healthy := true
		var issues []string
		var remedies []string

		if diag != nil {
			report, err := diag.Diagnose(ctx, params.Namespace, params.Name)
			if err != nil {
				return &mcpruntime.CallToolResult{
					Content: json.RawMessage(fmt.Sprintf(`{"error": %q}`, err.Error())),
					IsError: true,
				}, nil
			}
			healthy = report.Healthy
			for _, iss := range report.Issues {
				issues = append(issues, fmt.Sprintf("[%s] %s: %s", iss.Severity, iss.Component, iss.Summary))
				if iss.Remediation != "" {
					remedies = append(remedies, iss.Remediation)
				}
			}
			remedies = append(remedies, report.Suggestions...)
		} else {
			resp, err := client.GetEnvironment(ctx, connect.NewRequest(&divergev1alpha1.GetEnvironmentRequest{
				Name:      params.Name,
				Namespace: params.Namespace,
			}))
			if err != nil {
				return &mcpruntime.CallToolResult{
					Content: json.RawMessage(fmt.Sprintf(`{"error": %q}`, err.Error())),
					IsError: true,
				}, nil
			}

			env := resp.Msg.Environment
			if env != nil && env.Status != nil {
				phase := env.Status.Phase
				if phase == "Failed" || phase == "Error" {
					healthy = false
					issues = append(issues, fmt.Sprintf("Environment phase is %s", phase))
					remedies = append(remedies, "Inspect logs with diverge_fetch_errors or verify container image tags")
				}
			}
		}

		data, _ := json.Marshal(map[string]interface{}{
			"name":      params.Name,
			"namespace": params.Namespace,
			"healthy":   healthy,
			"issues":    issues,
			"remedies":  remedies,
		})

		return &mcpruntime.CallToolResult{
			Content: json.RawMessage(data),
			IsError: !healthy,
		}, nil
	})
}
