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
				"namespace": "payments",
				"top_limit": 2,
			},
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError)
	require.NotNil(t, mockCluster.lastReq)
	assert.Equal(t, "payments", mockCluster.lastReq.Namespace)

	require.Len(t, res.Content, 1)
	textContent, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(textContent.Text), &decoded))
	assert.InDelta(t, 0.084, decoded["totalHourlyCostUsd"], 1e-6)
	consumers, ok := decoded["topConsumers"].([]any)
	require.True(t, ok)
	assert.Len(t, consumers, 2)
}

func TestMCPCostSummaryTool_NilClientAndErrorPropagation(t *testing.T) {
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
}
