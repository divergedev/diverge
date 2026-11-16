package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/protocgen/proto2mcp/pkg/mcpruntime"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	divergev1alpha1connect "github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
)

const defaultMCPCostTopLimit = 20

var costSummarySchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"namespace": {
			"type": "string",
			"description": "Optional Kubernetes namespace filter (omit or empty for cluster-wide summary)"
		},
		"top_limit": {
			"type": "integer",
			"description": "Maximum number of top PR/environment consumers to return (default: 20)"
		}
	},
	"additionalProperties": false
}`)

func registerCostSummaryTool(registry mcpruntime.Registry, clusterClient divergev1alpha1connect.ClusterServiceClient) {
	registry.Register(mcpruntime.ToolDefinition{
		Name:        "diverge_get_cost_summary",
		Description: "Get FinOps hourly burn, monthly run-rate, hibernation and delta-routing savings, AgentTask LLM spend, namespace quota utilization, and top PR/environment consumers.",
		InputSchema: costSummarySchema,
	}, func(ctx context.Context, req mcpruntime.ToolRequest) (*mcpruntime.CallToolResult, error) {
		var params struct {
			Namespace string `json:"namespace"`
			TopLimit  int    `json:"top_limit"`
		}
		if len(req.Arguments) > 0 {
			if err := json.Unmarshal(req.Arguments, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
		}
		if clusterClient == nil {
			return errorToolResult(errors.New("cost summary client is not configured")), nil
		}

		topLimit := params.TopLimit
		if topLimit <= 0 {
			topLimit = defaultMCPCostTopLimit
		}

		rpcCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		resp, err := clusterClient.GetCostSummary(rpcCtx, connect.NewRequest(&divergev1alpha1.GetCostSummaryRequest{
			Namespace: strings.TrimSpace(params.Namespace),
		}))
		if err != nil {
			return errorToolResult(err), nil
		}

		msg := proto.Clone(resp.Msg).(*divergev1alpha1.GetCostSummaryResponse)
		if len(msg.TopConsumers) > topLimit {
			msg.TopConsumers = msg.TopConsumers[:topLimit]
		}

		data, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(msg)
		if err != nil {
			return errorToolResult(err), nil
		}
		return &mcpruntime.CallToolResult{
			Content: json.RawMessage(data),
		}, nil
	})
}
