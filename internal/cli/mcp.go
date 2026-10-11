package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"unicode"

	"connectrpc.com/connect"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/protocgen/proto2mcp/pkg/mcpruntime"
	"github.com/spf13/cobra"

	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	divergev1alpha1connect "github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/pkg/doctor"
)

// pascalToSnake converts PascalCase to snake_case.
func pascalToSnake(s string) string {
	runes := []rune(s)
	var result strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) {
			// Insert underscore before this uppercase run if:
			// 1. Not at the start
			// 2. Either the previous char was lowercase, or the next char is lowercase
			//    (the latter handles "TTL" -> "ttl" but "TTLExpiry" -> "ttl_expiry")
			if i > 0 {
				prevLower := unicode.IsLower(runes[i-1])
				nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if prevLower || nextLower {
					result.WriteByte('_')
				}
			}
			result.WriteRune(unicode.ToLower(r))
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// divergeToolNamer produces tool names like "diverge_get_environment".
func divergeToolNamer(_, methodName string) string {
	return "diverge_" + pascalToSnake(methodName)
}

func newMCPCmd(app *App) *cobra.Command {
	var serverURL string
	var allowDestructive bool

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run MCP server over stdio for AI agent integration",
		Long: `Starts a Model Context Protocol (MCP) server over stdio,
exposing Diverge operations as tools that AI agents can call.

Configure in your AI editor (Cursor, Claude, etc.):
	"diverge": { "command": "diverge", "args": ["mcp"] }`,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCP(cmd.Context(), app, serverURL, allowDestructive)
		},
	}

	cmd.Flags().StringVar(&serverURL, "server-url", "", "Diverge server URL (auto-discovered if empty)")
	cmd.Flags().BoolVar(&allowDestructive, "allow-destructive", false, "Enable destructive tools (delete)")
	return cmd
}

type mcpEnvHandler struct {
	client divergev1alpha1connect.EnvironmentServiceClient
}

func (h *mcpEnvHandler) CreateEnvironment(ctx context.Context, req *divergev1alpha1.CreateEnvironmentRequest) (*divergev1alpha1.CreateEnvironmentResponse, error) {
	resp, err := h.client.CreateEnvironment(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpEnvHandler) GetEnvironment(ctx context.Context, req *divergev1alpha1.GetEnvironmentRequest) (*divergev1alpha1.GetEnvironmentResponse, error) {
	resp, err := h.client.GetEnvironment(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpEnvHandler) ListEnvironments(ctx context.Context, req *divergev1alpha1.ListEnvironmentsRequest) (*divergev1alpha1.ListEnvironmentsResponse, error) {
	resp, err := h.client.ListEnvironments(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpEnvHandler) UpdateEnvironment(ctx context.Context, req *divergev1alpha1.UpdateEnvironmentRequest) (*divergev1alpha1.UpdateEnvironmentResponse, error) {
	resp, err := h.client.UpdateEnvironment(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpEnvHandler) DeleteEnvironment(ctx context.Context, req *divergev1alpha1.DeleteEnvironmentRequest) (*divergev1alpha1.DeleteEnvironmentResponse, error) {
	resp, err := h.client.DeleteEnvironment(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpEnvHandler) ExtendTTL(ctx context.Context, req *divergev1alpha1.ExtendTTLRequest) (*divergev1alpha1.ExtendTTLResponse, error) {
	resp, err := h.client.ExtendTTL(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpEnvHandler) ListHookJobs(ctx context.Context, req *divergev1alpha1.ListHookJobsRequest) (*divergev1alpha1.ListHookJobsResponse, error) {
	resp, err := h.client.ListHookJobs(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpEnvHandler) RetryHook(ctx context.Context, req *divergev1alpha1.RetryHookRequest) (*divergev1alpha1.RetryHookResponse, error) {
	resp, err := h.client.RetryHook(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

type mcpPgHandler struct {
	client divergev1alpha1connect.PreviewGroupServiceClient
}

func (h *mcpPgHandler) CreatePreviewGroup(ctx context.Context, req *divergev1alpha1.CreatePreviewGroupRequest) (*divergev1alpha1.CreatePreviewGroupResponse, error) {
	resp, err := h.client.CreatePreviewGroup(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpPgHandler) GetPreviewGroup(ctx context.Context, req *divergev1alpha1.GetPreviewGroupRequest) (*divergev1alpha1.GetPreviewGroupResponse, error) {
	resp, err := h.client.GetPreviewGroup(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpPgHandler) ListPreviewGroups(ctx context.Context, req *divergev1alpha1.ListPreviewGroupsRequest) (*divergev1alpha1.ListPreviewGroupsResponse, error) {
	resp, err := h.client.ListPreviewGroups(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpPgHandler) UpdatePreviewGroup(ctx context.Context, req *divergev1alpha1.UpdatePreviewGroupRequest) (*divergev1alpha1.UpdatePreviewGroupResponse, error) {
	resp, err := h.client.UpdatePreviewGroup(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpPgHandler) DeletePreviewGroup(ctx context.Context, req *divergev1alpha1.DeletePreviewGroupRequest) (*divergev1alpha1.DeletePreviewGroupResponse, error) {
	resp, err := h.client.DeletePreviewGroup(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

type mcpTaskHandler struct {
	client divergev1alpha1connect.AgentTaskServiceClient
}

func (h *mcpTaskHandler) CreateTask(ctx context.Context, req *divergev1alpha1.CreateTaskRequest) (*divergev1alpha1.CreateTaskResponse, error) {
	resp, err := h.client.CreateTask(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpTaskHandler) GetTask(ctx context.Context, req *divergev1alpha1.GetTaskRequest) (*divergev1alpha1.GetTaskResponse, error) {
	resp, err := h.client.GetTask(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpTaskHandler) ListTasks(ctx context.Context, req *divergev1alpha1.ListTasksRequest) (*divergev1alpha1.ListTasksResponse, error) {
	resp, err := h.client.ListTasks(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpTaskHandler) DeleteTask(ctx context.Context, req *divergev1alpha1.DeleteTaskRequest) (*divergev1alpha1.DeleteTaskResponse, error) {
	resp, err := h.client.DeleteTask(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpTaskHandler) PauseTask(ctx context.Context, req *divergev1alpha1.PauseTaskRequest) (*divergev1alpha1.PauseTaskResponse, error) {
	resp, err := h.client.PauseTask(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpTaskHandler) ResumeTask(ctx context.Context, req *divergev1alpha1.ResumeTaskRequest) (*divergev1alpha1.ResumeTaskResponse, error) {
	resp, err := h.client.ResumeTask(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
func (h *mcpTaskHandler) GuideTask(ctx context.Context, req *divergev1alpha1.GuideTaskRequest) (*divergev1alpha1.GuideTaskResponse, error) {
	resp, err := h.client.GuideTask(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

type mcpToolMeta struct {
	description string
	annotations mcp.ToolAnnotation
}

var agentTaskToolMetadata = map[string]mcpToolMeta{
	"diverge_create_task": {
		description: "Create and launch an autonomous AgentTask in an isolated sandbox for a repository and objective.",
		annotations: mcp.ToolAnnotation{
			Title:           "Create Agent Task",
			ReadOnlyHint:    mcp.ToBoolPtr(false),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(false),
			OpenWorldHint:   mcp.ToBoolPtr(true),
		},
	},
	"diverge_get_task": {
		description: "Get the current specification, lifecycle phase, sandbox status, and cost/token usage of an AgentTask.",
		annotations: mcp.ToolAnnotation{
			Title:           "Get Agent Task",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		},
	},
	"diverge_list_tasks": {
		description: "List AgentTasks in a namespace with optional pagination.",
		annotations: mcp.ToolAnnotation{
			Title:           "List Agent Tasks",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		},
	},
	"diverge_delete_task": {
		description: "Delete an AgentTask and tear down its associated sandbox resources.",
		annotations: mcp.ToolAnnotation{
			Title:           "Delete Agent Task",
			ReadOnlyHint:    mcp.ToBoolPtr(false),
			DestructiveHint: mcp.ToBoolPtr(true),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		},
	},
	"diverge_pause_task": {
		description: "Pause execution of an active AgentTask and suspend its sandbox.",
		annotations: mcp.ToolAnnotation{
			Title:           "Pause Agent Task",
			ReadOnlyHint:    mcp.ToBoolPtr(false),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		},
	},
	"diverge_resume_task": {
		description: "Resume execution of a paused AgentTask, optionally bumping its USD or token budget.",
		annotations: mcp.ToolAnnotation{
			Title:           "Resume Agent Task",
			ReadOnlyHint:    mcp.ToBoolPtr(false),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(false),
			OpenWorldHint:   mcp.ToBoolPtr(true),
		},
	},
	"diverge_guide_task": {
		description: "Send real-time steering guidance or instructions to an active AgentTask.",
		annotations: mcp.ToolAnnotation{
			Title:           "Guide Agent Task",
			ReadOnlyHint:    mcp.ToBoolPtr(false),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(false),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		},
	},
}

func newMCPServer(envClient divergev1alpha1connect.EnvironmentServiceClient, pgClient divergev1alpha1connect.PreviewGroupServiceClient, taskClient divergev1alpha1connect.AgentTaskServiceClient, allowDestructive bool, diagnoser ...*doctor.Diagnoser) *server.MCPServer {
	registry := mcpruntime.NewToolRegistry()

	envHandler := &mcpEnvHandler{client: envClient}
	divergev1alpha1.RegisterEnvironmentServiceMCP(registry, envHandler, mcpruntime.WithToolNamer(divergeToolNamer))

	pgHandler := &mcpPgHandler{client: pgClient}
	divergev1alpha1.RegisterPreviewGroupServiceMCP(registry, pgHandler, mcpruntime.WithToolNamer(divergeToolNamer))

	if taskClient != nil {
		taskHandler := &mcpTaskHandler{client: taskClient}
		divergev1alpha1.RegisterAgentTaskServiceMCP(registry, taskHandler, mcpruntime.WithToolNamer(divergeToolNamer))
	}

	registerWaitForReady(registry, envClient)
	registerFetchErrors(registry, envClient)
	registerLoadtest(registry)
	registerDoctor(registry, envClient, diagnoser...)

	mcpServer := server.NewMCPServer("diverge", "1.0.0")

	for _, def := range registry.Tools() {
		toolName := def.Name
		if service, method, ok := strings.Cut(def.Name, "_"); ok && !strings.HasPrefix(def.Name, "diverge_") {
			toolName = divergeToolNamer(service, method)
		}

		// Skip streaming methods (MCP tools are request-response)
		if strings.Contains(toolName, "watch") || strings.Contains(toolName, "stream_") {
			continue
		}

		// Only register if allowed (simple destructive check)
		if !allowDestructive && strings.Contains(toolName, "delete") {
			continue
		}

		var toolSchema mcp.ToolInputSchema
		if err := json.Unmarshal(def.InputSchema, &toolSchema); err != nil {
			fmt.Fprintf(os.Stderr, "failed to unmarshal schema for tool %s: %v\n", toolName, err)
			continue
		}

		tool := mcp.Tool{
			Name:        toolName,
			Description: def.Description,
			InputSchema: toolSchema,
		}
		if meta, ok := agentTaskToolMetadata[toolName]; ok {
			if tool.Description == "" {
				tool.Description = meta.description
			}
			tool.Annotations = meta.annotations
		}

		lookupName := def.Name
		mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			handler, ok := registry.Lookup(lookupName)
			if !ok {
				handler, ok = registry.Lookup(request.Params.Name)
			}
			if !ok {
				return nil, fmt.Errorf("tool %s not found", request.Params.Name)
			}

			argsBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return nil, err
			}

			res, err := handler(ctx, mcpruntime.ToolRequest{
				ToolName:  request.Params.Name,
				Arguments: argsBytes,
			})
			if err != nil {
				return &mcp.CallToolResult{
					IsError: true,
					Content: []mcp.Content{
						mcp.TextContent{
							Type: "text",
							Text: err.Error(),
						},
					},
				}, nil
			}

			if res.IsError {
				return &mcp.CallToolResult{
					IsError: true,
					Content: []mcp.Content{
						mcp.TextContent{
							Type: "text",
							Text: string(res.Content),
						},
					},
				}, nil
			}

			return &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: string(res.Content),
					},
				},
			}, nil
		})
	}

	return mcpServer
}

const defaultMCPTokenFilePath = "/etc/diverge/token/token"

type bearerAuthRoundTripper struct {
	base         http.RoundTripper
	tokenFunc    func() string
	targetScheme string
	targetHost   string
}

// matchesTargetOrigin reports whether u shares the configured server's scheme and host.
func (rt *bearerAuthRoundTripper) matchesTargetOrigin(u *url.URL) bool {
	if u == nil || rt.targetScheme == "" || rt.targetHost == "" {
		return false
	}
	return strings.EqualFold(u.Scheme, rt.targetScheme) && strings.EqualFold(u.Host, rt.targetHost)
}

// RoundTrip attaches the resolved bearer token to same-origin requests over HTTPS (or loopback HTTP)
// when no Authorization header is already present.
func (rt *bearerAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := rt.base
	if base == nil {
		base = http.DefaultTransport
	}
	if rt.tokenFunc != nil && req.Header.Get("Authorization") == "" && rt.matchesTargetOrigin(req.URL) {
		if token := rt.tokenFunc(); token != "" {
			// Security: Do not transmit bearer credentials in cleartext over non-loopback HTTP.
			if strings.EqualFold(req.URL.Scheme, "http") && !isLoopbackHost(req.URL.Host) {
				return nil, fmt.Errorf("insecure HTTP scheme is only allowed for loopback addresses, got %q", req.URL.Host)
			}
			if !strings.EqualFold(req.URL.Scheme, "https") && !strings.EqualFold(req.URL.Scheme, "http") {
				return nil, fmt.Errorf("unsupported URL scheme %q for bearer token authentication", req.URL.Scheme)
			}
			clone := req.Clone(req.Context())
			clone.Header.Set("Authorization", "Bearer "+token)
			req = clone
		}
	}
	return base.RoundTrip(req)
}

// resolveMCPBearerToken reads the MCP bearer token from DIVERGE_TOKEN or the mounted token file.
func resolveMCPBearerToken() string {
	if token := strings.TrimSpace(os.Getenv("DIVERGE_TOKEN")); token != "" {
		return token
	}
	tokenFile := strings.TrimSpace(os.Getenv("DIVERGE_TOKEN_FILE"))
	if tokenFile == "" {
		tokenFile = defaultMCPTokenFilePath
	}
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// newMCPHTTPClient constructs an *http.Client that injects bearer credentials for the target server origin.
func newMCPHTTPClient(serverURL string) *http.Client {
	var targetScheme, targetHost string
	if parsed, err := url.Parse(strings.TrimSpace(serverURL)); err == nil && parsed != nil {
		targetScheme = parsed.Scheme
		targetHost = parsed.Host
	}
	return &http.Client{
		Transport: &bearerAuthRoundTripper{
			base:         http.DefaultTransport,
			tokenFunc:    resolveMCPBearerToken,
			targetScheme: targetScheme,
			targetHost:   targetHost,
		},
	}
}

// runMCP starts the MCP stdio server connected to the given Diverge API server URL.
func runMCP(ctx context.Context, app *App, serverURL string, allowDestructive bool) error {
	if serverURL == "" {
		serverURL = os.Getenv("DIVERGE_SERVER_URL")
		if serverURL == "" {
			return fmt.Errorf("server URL required: use --server-url or set DIVERGE_SERVER_URL")
		}
	}

	httpClient := newMCPHTTPClient(serverURL)
	envClient := divergev1alpha1connect.NewEnvironmentServiceClient(httpClient, serverURL)
	pgClient := divergev1alpha1connect.NewPreviewGroupServiceClient(httpClient, serverURL)
	taskClient := divergev1alpha1connect.NewAgentTaskServiceClient(httpClient, serverURL)

	var diagnoser *doctor.Diagnoser
	if c, _, err := app.KubeClient(); err == nil && c != nil {
		diagnoser = doctor.NewDiagnoser(c)
	}

	mcpServer := newMCPServer(envClient, pgClient, taskClient, allowDestructive, diagnoser)

	return server.ServeStdio(mcpServer)
}
