package cli

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
)

type mockPgClient struct{}

func (m *mockPgClient) CreatePreviewGroup(ctx context.Context, req *connect.Request[divergev1alpha1.CreatePreviewGroupRequest]) (*connect.Response[divergev1alpha1.CreatePreviewGroupResponse], error) {
	return nil, nil
}
func (m *mockPgClient) GetPreviewGroup(ctx context.Context, req *connect.Request[divergev1alpha1.GetPreviewGroupRequest]) (*connect.Response[divergev1alpha1.GetPreviewGroupResponse], error) {
	return nil, nil
}
func (m *mockPgClient) ListPreviewGroups(ctx context.Context, req *connect.Request[divergev1alpha1.ListPreviewGroupsRequest]) (*connect.Response[divergev1alpha1.ListPreviewGroupsResponse], error) {
	return nil, nil
}
func (m *mockPgClient) UpdatePreviewGroup(ctx context.Context, req *connect.Request[divergev1alpha1.UpdatePreviewGroupRequest]) (*connect.Response[divergev1alpha1.UpdatePreviewGroupResponse], error) {
	return nil, nil
}
func (m *mockPgClient) DeletePreviewGroup(ctx context.Context, req *connect.Request[divergev1alpha1.DeletePreviewGroupRequest]) (*connect.Response[divergev1alpha1.DeletePreviewGroupResponse], error) {
	return nil, nil
}
func (m *mockPgClient) WatchPreviewGroups(ctx context.Context, req *connect.Request[divergev1alpha1.WatchPreviewGroupsRequest]) (*connect.ServerStreamForClient[divergev1alpha1.WatchPreviewGroupsResponse], error) {
	return nil, nil
}

func TestPascalToSnake(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"GetEnvironment", "get_environment"},
		{"CreatePreviewGroup", "create_preview_group"},
		{"WatchEnvironments", "watch_environments"},
		{"StreamLogs", "stream_logs"},
		{"ABCTest", "abc_test"},
		{"ExtendTTL", "extend_ttl"},
		{"ListHTTPRoutes", "list_http_routes"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, pascalToSnake(tt.input))
		})
	}
}

func TestDivergeToolNamer(t *testing.T) {
	assert.Equal(t, "diverge_get_environment", divergeToolNamer("EnvironmentService", "GetEnvironment"))
	assert.Equal(t, "diverge_extend_ttl", divergeToolNamer("EnvironmentService", "ExtendTTL"))
	assert.Equal(t, "diverge_create_preview_group", divergeToolNamer("PreviewGroupService", "CreatePreviewGroup"))
}

func TestMCPToolRegistration(t *testing.T) {
	server := newMCPServer(&mockEnvClient{}, &mockPgClient{}, true)
	require.NotNil(t, server)

	tools := server.ListTools()

	// Verify all expected EnvironmentService tools are registered
	expectedEnvTools := []string{
		"diverge_create_environment",
		"diverge_get_environment",
		"diverge_list_environments",
		"diverge_update_environment",
		"diverge_delete_environment",
		divergeToolNamer("EnvironmentService", "ExtendTTL"),
		"diverge_list_hook_jobs",
		"diverge_retry_hook",
		"diverge_wait_for_ready",
		"diverge_fetch_errors",
	}
	for _, name := range expectedEnvTools {
		assert.Contains(t, tools, name, "expected EnvironmentService tool %s to be registered", name)
	}

	// Verify all expected PreviewGroupService tools are registered
	expectedPgTools := []string{
		"diverge_create_preview_group",
		"diverge_get_preview_group",
		"diverge_list_preview_groups",
		"diverge_update_preview_group",
		"diverge_delete_preview_group",
	}
	for _, name := range expectedPgTools {
		assert.Contains(t, tools, name, "expected PreviewGroupService tool %s to be registered", name)
	}

	// Verify Auth, Cluster, and Tunnel services are NOT registered
	unregisteredSubstrings := []string{"auth", "login", "logout", "token", "cluster", "tunnel"}
	for name := range tools {
		for _, substr := range unregisteredSubstrings {
			assert.False(t, strings.Contains(name, substr), "tool %s should not belong to Auth, Cluster, or Tunnel service", name)
		}
	}

	// Verify streaming methods (WatchEnvironments, StreamLogs, WatchPreviewGroups) are NOT registered
	streamingToolNames := []string{
		"diverge_watch_environments",
		"diverge_stream_logs",
		"diverge_watch_preview_groups",
	}
	for _, name := range streamingToolNames {
		assert.NotContains(t, tools, name, "streaming method %s must not be registered as an MCP tool", name)
	}
}

func TestMCPDestructiveFiltering(t *testing.T) {
	t.Run("without allow-destructive", func(t *testing.T) {
		server := newMCPServer(&mockEnvClient{}, &mockPgClient{}, false)
		tools := server.ListTools()

		// Verify DeleteEnvironment and DeletePreviewGroup are excluded
		assert.NotContains(t, tools, "diverge_delete_environment")
		assert.NotContains(t, tools, "diverge_delete_preview_group")

		// Verify non-destructive tools are still present
		assert.Contains(t, tools, "diverge_get_environment")
		assert.Contains(t, tools, "diverge_get_preview_group")
		assert.Contains(t, tools, "diverge_create_environment")
		assert.Contains(t, tools, "diverge_create_preview_group")
	})

	t.Run("with allow-destructive", func(t *testing.T) {
		server := newMCPServer(&mockEnvClient{}, &mockPgClient{}, true)
		tools := server.ListTools()

		// Verify DeleteEnvironment and DeletePreviewGroup are included
		assert.Contains(t, tools, "diverge_delete_environment")
		assert.Contains(t, tools, "diverge_delete_preview_group")
	})
}

func TestMCPToolNaming(t *testing.T) {
	// Specific examples from spec
	assert.Equal(t, "diverge_create_environment", divergeToolNamer("EnvironmentService", "CreateEnvironment"))
	assert.Equal(t, "diverge_get_preview_group", divergeToolNamer("PreviewGroupService", "GetPreviewGroup"))

	// Test all registered tools follow diverge_<snake_case> convention
	server := newMCPServer(&mockEnvClient{}, &mockPgClient{}, true)
	tools := server.ListTools()
	require.NotEmpty(t, tools)

	snakeCasePattern := regexp.MustCompile(`^diverge_[a-z0-9]+(_[a-z0-9]+)*$`)
	for name := range tools {
		assert.True(t, strings.HasPrefix(name, "diverge_"), "tool %s should start with 'diverge_'", name)
		assert.True(t, snakeCasePattern.MatchString(name), "tool %s should follow diverge_<snake_case> convention", name)
	}
}
