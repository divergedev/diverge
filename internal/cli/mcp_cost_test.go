package cli

import (
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/protocgen/proto2mcp/pkg/mcpruntime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
)

type mockClusterClient struct {
	resp    *divergev1alpha1.GetCostSummaryResponse
	err     error
	lastReq *divergev1alpha1.GetCostSummaryRequest
}

func (m *mockClusterClient) GetClusterInfo(_ context.Context, _ *connect.Request[divergev1alpha1.GetClusterInfoRequest]) (*connect.Response[divergev1alpha1.GetClusterInfoResponse], error) {
	return nil, m.err
}

func (m *mockClusterClient) GetCostSummary(_ context.Context, req *connect.Request[divergev1alpha1.GetCostSummaryRequest]) (*connect.Response[divergev1alpha1.GetCostSummaryResponse], error) {
	m.lastReq = req.Msg
	if m.err != nil {
		return nil, m.err
	}
	return connect.NewResponse(m.resp), nil
}

func TestMCPCostSummaryTool_RegisteredAndTruncatesTopConsumers(t *testing.T) {
	mockCluster := &mockClusterClient{
		resp: &divergev1alpha1.GetCostSummaryResponse{
			TotalHourlyCostUsd:          0.084,
			MonthlyRunRateUsd:           61.32,
			HibernationSavingsHourlyUsd: 0.024,
			ActiveEnvironments:          2,
			HibernatedEnvironments:      1,
			TopConsumers: []*divergev1alpha1.EnvironmentCostItem{
				{Name: "pr-1", Namespace: "payments", HourlyCostUsd: 0.048},
				{Name: "pr-2", Namespace: "payments", HourlyCostUsd: 0.036},
				{Name: "pr-3", Namespace: "payments", HourlyCostUsd: 0.000, Hibernated: true},
			},
			HistoricalConsumers: []*divergev1alpha1.EnvironmentCostItem{
				{Name: "pr-10", Namespace: "payments", Phase: "Closed", AccruedCostUsd: 1.25},
				{Name: "pr-11", Namespace: "payments", Phase: "Closed", AccruedCostUsd: 0.90},
				{Name: "pr-12", Namespace: "payments", Phase: "Closed", AccruedCostUsd: 0.45},
			},
		},
	}

	mcpSrv := newMCPServerWithCluster(&mockEnvClient{}, &mockPgClient{}, &mockTaskClient{}, mockCluster, false)
	tools := mcpSrv.ListTools()
	srvTool, ok := tools["diverge_get_cost_summary"]
	require.True(t, ok, "diverge_get_cost_summary should be registered in non-destructive mode")
	require.NotNil(t, srvTool.Tool.Annotations.ReadOnlyHint)
	assert.True(t, *srvTool.Tool.Annotations.ReadOnlyHint)
	require.NotNil(t, srvTool.Tool.Annotations.DestructiveHint)
	assert.False(t, *srvTool.Tool.Annotations.DestructiveHint)

	res, err := srvTool.Handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "diverge_get_cost_summary",
			Arguments: map[string]any{
				"namespace":          "payments",
				"top_limit":          2,
				"include_historical": true,
				"window_days":        7,
			},
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError)
	require.NotNil(t, mockCluster.lastReq)
	assert.Equal(t, "payments", mockCluster.lastReq.Namespace)
	assert.True(t, mockCluster.lastReq.IncludeHistorical)
	assert.Equal(t, int32(7), mockCluster.lastReq.WindowDays)

	require.Len(t, res.Content, 1)
	textContent, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(textContent.Text), &decoded))
	assert.InDelta(t, 0.084, decoded["totalHourlyCostUsd"], 1e-6)
	consumers, ok := decoded["topConsumers"].([]any)
	require.True(t, ok)
	assert.Len(t, consumers, 2)
	histConsumers, ok := decoded["historicalConsumers"].([]any)
	require.True(t, ok)
	assert.Len(t, histConsumers, 2)

	// top_limit: 0 returns unlimited consumers
	resUnlimited, err := srvTool.Handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "diverge_get_cost_summary",
			Arguments: map[string]any{
				"top_limit":          0,
				"include_historical": true,
			},
		},
	})
	require.NoError(t, err)
	assert.False(t, resUnlimited.IsError)
	var decodedUnlimited map[string]any
	require.NoError(t, json.Unmarshal([]byte(resUnlimited.Content[0].(mcp.TextContent).Text), &decodedUnlimited))
	assert.Len(t, decodedUnlimited["topConsumers"].([]any), 3)
	assert.Len(t, decodedUnlimited["historicalConsumers"].([]any), 3)
}

func TestMCPCostSummaryTool_ValidationAndErrorPropagation(t *testing.T) {
	reg := mcpruntime.NewToolRegistry()
	registerCostSummaryTool(reg, nil)
	handler, ok := reg.Lookup("diverge_get_cost_summary")
	require.True(t, ok)

	res, err := handler(context.Background(), mcpruntime.ToolRequest{
		ToolName:  "diverge_get_cost_summary",
		Arguments: []byte(`{}`),
	})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, string(res.Content), "not configured")

	regWithClient := mcpruntime.NewToolRegistry()
	registerCostSummaryTool(regWithClient, &mockClusterClient{resp: &divergev1alpha1.GetCostSummaryResponse{}})
	h, ok := regWithClient.Lookup("diverge_get_cost_summary")
	require.True(t, ok)

	badWindow, err := h(context.Background(), mcpruntime.ToolRequest{
		ToolName:  "diverge_get_cost_summary",
		Arguments: []byte(`{"window_days": 90}`),
	})
	require.NoError(t, err)
	assert.True(t, badWindow.IsError)
	assert.Contains(t, string(badWindow.Content), "window_days must be between 0 and 30")

	badLimit, err := h(context.Background(), mcpruntime.ToolRequest{
		ToolName:  "diverge_get_cost_summary",
		Arguments: []byte(`{"top_limit": -1}`),
	})
	require.NoError(t, err)
	assert.True(t, badLimit.IsError)
	var errPayload map[string]string
	require.NoError(t, json.Unmarshal(badLimit.Content, &errPayload))
	assert.Contains(t, errPayload["error"], "top_limit must be >= 0")
}
