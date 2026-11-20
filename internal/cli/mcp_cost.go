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
	"github.com/divergedev/diverge/internal/finops"
)

const defaultMCPCostTopLimit = 20

const costSummaryToolDescription = "Get FinOps hourly burn, monthly run-rate, hibernation and delta-routing savings, AgentTask LLM spend, namespace quota utilization, 7d/30d daily trend analytics, and live or historical PR/environment consumers."

var costSummarySchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"namespace": {
			"type": "string",
			"description": "Optional Kubernetes namespace filter (omit or empty for cluster-wide summary)"
		},
		"top_limit": {
			"type": "integer",
			"description": "Maximum number of live and historical PR/environment consumers to return (default: 20, 0 for unlimited)"
		},
		"include_historical": {
			"type": "boolean",
			"description": "Include closed/deleted preview environments from the 30-day historical FinOps ledger (default: false)"
		},
		"window_days": {
			"type": "integer",
			"description": "Historical FinOps daily trend and ledger lookback window in UTC days (1-30; 0 defaults to 30)"
		}
	},
	"additionalProperties": false
}`)

func registerCostSummaryTool(registry mcpruntime.Registry, clusterClient divergev1alpha1connect.ClusterServiceClient) {
	registry.Register(mcpruntime.ToolDefinition{
		Name:        "diverge_get_cost_summary",
		Description: costSummaryToolDescription,
		InputSchema: costSummarySchema,
	}, func(ctx context.Context, req mcpruntime.ToolRequest) (*mcpruntime.CallToolResult, error) {
		var params struct {
			Namespace         string `json:"namespace"`
			TopLimit          *int   `json:"top_limit"`
			IncludeHistorical bool   `json:"include_historical"`
			WindowDays        int    `json:"window_days"`
		}
		if len(req.Arguments) > 0 {
			if err := json.Unmarshal(req.Arguments, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
		}
		if clusterClient == nil {
			return errorToolResult(errors.New("cost summary client is not configured")), nil
		}

		topLimit := defaultMCPCostTopLimit
		if params.TopLimit != nil {
			if *params.TopLimit < 0 {
				return errorToolResult(fmt.Errorf("top_limit must be >= 0, got %d", *params.TopLimit)), nil
			}
			topLimit = *params.TopLimit
		}
		if params.WindowDays < 0 || params.WindowDays > finops.LedgerRetentionDays {
			return errorToolResult(fmt.Errorf("window_days must be between 0 and %d, got %d", finops.LedgerRetentionDays, params.WindowDays)), nil
		}

		rpcCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		resp, err := clusterClient.GetCostSummary(rpcCtx, connect.NewRequest(&divergev1alpha1.GetCostSummaryRequest{
			Namespace:         strings.TrimSpace(params.Namespace),
			IncludeHistorical: params.IncludeHistorical,
			WindowDays:        int32(params.WindowDays),
		}))
		if err != nil {
			return errorToolResult(err), nil
		}

		msg := proto.Clone(resp.Msg).(*divergev1alpha1.GetCostSummaryResponse)
		if topLimit > 0 {
			if len(msg.TopConsumers) > topLimit {
				msg.TopConsumers = msg.TopConsumers[:topLimit]
			}
			if len(msg.HistoricalConsumers) > topLimit {
				msg.HistoricalConsumers = msg.HistoricalConsumers[:topLimit]
			}
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
