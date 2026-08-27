package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/protocgen/proto2mcp/pkg/mcpruntime"

	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	divergev1alpha1connect "github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
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
