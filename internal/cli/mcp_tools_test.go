package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/protocgen/proto2mcp/pkg/mcpruntime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
)

type mockEnvClient struct {
	getEnvResponse *divergev1alpha1.GetEnvironmentResponse
	getEnvErr      error

	streamLogsErr error
}

func (m *mockEnvClient) CreateEnvironment(ctx context.Context, req *connect.Request[divergev1alpha1.CreateEnvironmentRequest]) (*connect.Response[divergev1alpha1.CreateEnvironmentResponse], error) {
	return nil, nil
}
func (m *mockEnvClient) GetEnvironment(ctx context.Context, req *connect.Request[divergev1alpha1.GetEnvironmentRequest]) (*connect.Response[divergev1alpha1.GetEnvironmentResponse], error) {
	if m.getEnvErr != nil {
		return nil, m.getEnvErr
	}
	return connect.NewResponse(m.getEnvResponse), nil
}
func (m *mockEnvClient) ListEnvironments(ctx context.Context, req *connect.Request[divergev1alpha1.ListEnvironmentsRequest]) (*connect.Response[divergev1alpha1.ListEnvironmentsResponse], error) {
	return nil, nil
}
func (m *mockEnvClient) UpdateEnvironment(ctx context.Context, req *connect.Request[divergev1alpha1.UpdateEnvironmentRequest]) (*connect.Response[divergev1alpha1.UpdateEnvironmentResponse], error) {
	return nil, nil
}
func (m *mockEnvClient) DeleteEnvironment(ctx context.Context, req *connect.Request[divergev1alpha1.DeleteEnvironmentRequest]) (*connect.Response[divergev1alpha1.DeleteEnvironmentResponse], error) {
	return nil, nil
}
func (m *mockEnvClient) ExtendTTL(ctx context.Context, req *connect.Request[divergev1alpha1.ExtendTTLRequest]) (*connect.Response[divergev1alpha1.ExtendTTLResponse], error) {
	return nil, nil
}
func (m *mockEnvClient) WatchEnvironments(ctx context.Context, req *connect.Request[divergev1alpha1.WatchEnvironmentsRequest]) (*connect.ServerStreamForClient[divergev1alpha1.WatchEnvironmentsResponse], error) {
	return nil, nil
}
func (m *mockEnvClient) StreamLogs(ctx context.Context, req *connect.Request[divergev1alpha1.StreamLogsRequest]) (*connect.ServerStreamForClient[divergev1alpha1.StreamLogsResponse], error) {
	return nil, m.streamLogsErr // For simplicity, we just won't implement the full stream mock in this stub unless we need to. I'll mock it if needed.
}
func (m *mockEnvClient) ListHookJobs(ctx context.Context, req *connect.Request[divergev1alpha1.ListHookJobsRequest]) (*connect.Response[divergev1alpha1.ListHookJobsResponse], error) {
	return nil, nil
}
func (m *mockEnvClient) RetryHook(ctx context.Context, req *connect.Request[divergev1alpha1.RetryHookRequest]) (*connect.Response[divergev1alpha1.RetryHookResponse], error) {
	return nil, nil
}

func TestWaitForReady(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()

	client := &mockEnvClient{
		getEnvResponse: &divergev1alpha1.GetEnvironmentResponse{
			Environment: &divergev1alpha1.Environment{
				Name:      "test-env",
				Namespace: "default",
				Status: &divergev1alpha1.EnvironmentStatus{
					Phase: "Ready",
				},
			},
		},
	}

	registerWaitForReady(registry, client)

	handler, ok := registry.Lookup("diverge_wait_for_ready")
	require.True(t, ok)

	args := []byte(`{"name": "test-env", "namespace": "default"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := handler(ctx, mcpruntime.ToolRequest{
		ToolName:  "diverge_wait_for_ready",
		Arguments: args,
	})
	require.NoError(t, err)
	assert.False(t, res.IsError)

	var data map[string]interface{}
	err = json.Unmarshal(res.Content, &data)
	require.NoError(t, err)
	assert.Equal(t, "Ready", data["phase"])
}

func TestContainsErrorLevel(t *testing.T) {
	assert.True(t, containsErrorLevel("this is an error line"))
	assert.True(t, containsErrorLevel("FATAL failure"))
	assert.True(t, containsErrorLevel("panic: runtime error"))
	assert.False(t, containsErrorLevel("info: starting up"))
}
