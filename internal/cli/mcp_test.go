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

type mockTaskClient struct {
	err error
}

func (m *mockTaskClient) CreateTask(ctx context.Context, req *connect.Request[divergev1alpha1.CreateTaskRequest]) (*connect.Response[divergev1alpha1.CreateTaskResponse], error) {
	if m.err != nil {
		return nil, m.err
	}
	return connect.NewResponse(&divergev1alpha1.CreateTaskResponse{
		Task: &divergev1alpha1.AgentTask{Name: req.Msg.Name, Namespace: req.Msg.Namespace},
	}), nil
}
func (m *mockTaskClient) GetTask(ctx context.Context, req *connect.Request[divergev1alpha1.GetTaskRequest]) (*connect.Response[divergev1alpha1.GetTaskResponse], error) {
	if m.err != nil {
		return nil, m.err
	}
	return connect.NewResponse(&divergev1alpha1.GetTaskResponse{
		Task: &divergev1alpha1.AgentTask{Name: req.Msg.Name, Namespace: req.Msg.Namespace},
	}), nil
}
func (m *mockTaskClient) ListTasks(ctx context.Context, req *connect.Request[divergev1alpha1.ListTasksRequest]) (*connect.Response[divergev1alpha1.ListTasksResponse], error) {
	if m.err != nil {
		return nil, m.err
	}
	return connect.NewResponse(&divergev1alpha1.ListTasksResponse{}), nil
}
func (m *mockTaskClient) DeleteTask(ctx context.Context, req *connect.Request[divergev1alpha1.DeleteTaskRequest]) (*connect.Response[divergev1alpha1.DeleteTaskResponse], error) {
	if m.err != nil {
		return nil, m.err
	}
	return connect.NewResponse(&divergev1alpha1.DeleteTaskResponse{}), nil
}
func (m *mockTaskClient) PauseTask(ctx context.Context, req *connect.Request[divergev1alpha1.PauseTaskRequest]) (*connect.Response[divergev1alpha1.PauseTaskResponse], error) {
	if m.err != nil {
		return nil, m.err
	}
	return connect.NewResponse(&divergev1alpha1.PauseTaskResponse{
		Task: &divergev1alpha1.AgentTask{Name: req.Msg.Name, Namespace: req.Msg.Namespace},
	}), nil
}
func (m *mockTaskClient) ResumeTask(ctx context.Context, req *connect.Request[divergev1alpha1.ResumeTaskRequest]) (*connect.Response[divergev1alpha1.ResumeTaskResponse], error) {
	if m.err != nil {
		return nil, m.err
	}
	return connect.NewResponse(&divergev1alpha1.ResumeTaskResponse{
		Task: &divergev1alpha1.AgentTask{Name: req.Msg.Name, Namespace: req.Msg.Namespace},
	}), nil
}
func (m *mockTaskClient) GuideTask(ctx context.Context, req *connect.Request[divergev1alpha1.GuideTaskRequest]) (*connect.Response[divergev1alpha1.GuideTaskResponse], error) {
	if m.err != nil {
		return nil, m.err
	}
	return connect.NewResponse(&divergev1alpha1.GuideTaskResponse{}), nil
}
func (m *mockTaskClient) StreamTaskLogs(ctx context.Context, req *connect.Request[divergev1alpha1.StreamTaskLogsRequest]) (*connect.ServerStreamForClient[divergev1alpha1.StreamTaskLogsResponse], error) {
	return nil, m.err
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
	assert.Equal(t, "diverge_create_task", divergeToolNamer("AgentTaskService", "CreateTask"))
}

func TestMCPToolRegistration(t *testing.T) {
	server := newMCPServer(&mockEnvClient{}, &mockPgClient{}, &mockTaskClient{}, true)
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

	// Verify all expected AgentTaskService tools are registered
	expectedTaskTools := []string{
		"diverge_create_task",
		"diverge_get_task",
		"diverge_list_tasks",
		"diverge_delete_task",
		"diverge_pause_task",
		"diverge_resume_task",
		"diverge_guide_task",
	}
	for _, name := range expectedTaskTools {
		assert.Contains(t, tools, name, "expected AgentTaskService tool %s to be registered", name)
	}

	// Verify Auth, Cluster, and Tunnel services are NOT registered
	unregisteredSubstrings := []string{"auth", "login", "logout", "token", "cluster", "tunnel"}
	for name := range tools {
		for _, substr := range unregisteredSubstrings {
			assert.False(t, strings.Contains(name, substr), "tool %s should not belong to Auth, Cluster, or Tunnel service", name)
		}
	}

	// Verify streaming methods (WatchEnvironments, StreamLogs, WatchPreviewGroups, StreamTaskLogs) are NOT registered
	streamingToolNames := []string{
		"diverge_watch_environments",
		"diverge_stream_logs",
		"diverge_watch_preview_groups",
		"diverge_stream_task_logs",
	}
	for _, name := range streamingToolNames {
		assert.NotContains(t, tools, name, "streaming method %s must not be registered as an MCP tool", name)
	}
}

func TestMCPDestructiveFiltering(t *testing.T) {
	t.Run("without allow-destructive", func(t *testing.T) {
		server := newMCPServer(&mockEnvClient{}, &mockPgClient{}, &mockTaskClient{}, false)
		tools := server.ListTools()

		// Verify DeleteEnvironment, DeletePreviewGroup, and DeleteTask are excluded
		assert.NotContains(t, tools, "diverge_delete_environment")
		assert.NotContains(t, tools, "diverge_delete_preview_group")
		assert.NotContains(t, tools, "diverge_delete_task")

		// Verify non-destructive tools are still present
		assert.Contains(t, tools, "diverge_get_environment")
		assert.Contains(t, tools, "diverge_get_preview_group")
		assert.Contains(t, tools, "diverge_get_task")
		assert.Contains(t, tools, "diverge_create_environment")
		assert.Contains(t, tools, "diverge_create_preview_group")
		assert.Contains(t, tools, "diverge_create_task")
	})

	t.Run("with allow-destructive", func(t *testing.T) {
		server := newMCPServer(&mockEnvClient{}, &mockPgClient{}, &mockTaskClient{}, true)
		tools := server.ListTools()

		// Verify DeleteEnvironment, DeletePreviewGroup, and DeleteTask are included
		assert.Contains(t, tools, "diverge_delete_environment")
		assert.Contains(t, tools, "diverge_delete_preview_group")
		assert.Contains(t, tools, "diverge_delete_task")
	})
}

func TestMCPToolNaming(t *testing.T) {
	// Specific examples from spec
	assert.Equal(t, "diverge_create_environment", divergeToolNamer("EnvironmentService", "CreateEnvironment"))
	assert.Equal(t, "diverge_get_preview_group", divergeToolNamer("PreviewGroupService", "GetPreviewGroup"))

	// Test all registered tools follow diverge_<snake_case> convention
	server := newMCPServer(&mockEnvClient{}, &mockPgClient{}, &mockTaskClient{}, true)
	tools := server.ListTools()
	require.NotEmpty(t, tools)

	snakeCasePattern := regexp.MustCompile(`^diverge_[a-z0-9]+(_[a-z0-9]+)*$`)
	for name := range tools {
		assert.True(t, strings.HasPrefix(name, "diverge_"), "tool %s should start with 'diverge_'", name)
		assert.True(t, snakeCasePattern.MatchString(name), "tool %s should follow diverge_<snake_case> convention", name)
	}
}

func TestMCPTaskHandler(t *testing.T) {
	ctx := context.Background()
	h := &mcpTaskHandler{client: &mockTaskClient{}}

	createResp, err := h.CreateTask(ctx, &divergev1alpha1.CreateTaskRequest{Name: "t1", Namespace: "ns1"})
	require.NoError(t, err)
	assert.Equal(t, "t1", createResp.Task.Name)

	getResp, err := h.GetTask(ctx, &divergev1alpha1.GetTaskRequest{Name: "t1", Namespace: "ns1"})
	require.NoError(t, err)
	assert.Equal(t, "t1", getResp.Task.Name)

	_, err = h.ListTasks(ctx, &divergev1alpha1.ListTasksRequest{Namespace: "ns1"})
	require.NoError(t, err)

	_, err = h.DeleteTask(ctx, &divergev1alpha1.DeleteTaskRequest{Name: "t1", Namespace: "ns1"})
	require.NoError(t, err)

	pauseResp, err := h.PauseTask(ctx, &divergev1alpha1.PauseTaskRequest{Name: "t1", Namespace: "ns1"})
	require.NoError(t, err)
	assert.Equal(t, "t1", pauseResp.Task.Name)

	resumeResp, err := h.ResumeTask(ctx, &divergev1alpha1.ResumeTaskRequest{Name: "t1", Namespace: "ns1"})
	require.NoError(t, err)
	assert.Equal(t, "t1", resumeResp.Task.Name)

	_, err = h.GuideTask(ctx, &divergev1alpha1.GuideTaskRequest{Name: "t1", Namespace: "ns1", Message: "focus on tests"})
	require.NoError(t, err)

	// Verify error propagation
	errHandler := &mcpTaskHandler{client: &mockTaskClient{err: assert.AnError}}
	_, err = errHandler.CreateTask(ctx, &divergev1alpha1.CreateTaskRequest{})
	assert.ErrorIs(t, err, assert.AnError)
	_, err = errHandler.GetTask(ctx, &divergev1alpha1.GetTaskRequest{})
	assert.ErrorIs(t, err, assert.AnError)
	_, err = errHandler.ListTasks(ctx, &divergev1alpha1.ListTasksRequest{})
	assert.ErrorIs(t, err, assert.AnError)
	_, err = errHandler.DeleteTask(ctx, &divergev1alpha1.DeleteTaskRequest{})
	assert.ErrorIs(t, err, assert.AnError)
	_, err = errHandler.PauseTask(ctx, &divergev1alpha1.PauseTaskRequest{})
	assert.ErrorIs(t, err, assert.AnError)
	_, err = errHandler.ResumeTask(ctx, &divergev1alpha1.ResumeTaskRequest{})
	assert.ErrorIs(t, err, assert.AnError)
	_, err = errHandler.GuideTask(ctx, &divergev1alpha1.GuideTaskRequest{})
	assert.ErrorIs(t, err, assert.AnError)
}
