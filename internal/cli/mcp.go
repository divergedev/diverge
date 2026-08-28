package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// Stub out the skipped streaming methods that were temporarily un-streamed
func (h *mcpEnvHandler) WatchEnvironments(ctx context.Context, req *divergev1alpha1.WatchEnvironmentsRequest) (*divergev1alpha1.WatchEnvironmentsResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (h *mcpEnvHandler) StreamLogs(ctx context.Context, req *divergev1alpha1.StreamLogsRequest) (*divergev1alpha1.StreamLogsResponse, error) {
	return nil, fmt.Errorf("not implemented")
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
func (h *mcpPgHandler) WatchPreviewGroups(ctx context.Context, req *divergev1alpha1.WatchPreviewGroupsRequest) (*divergev1alpha1.WatchPreviewGroupsResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func newMCPServer(envClient divergev1alpha1connect.EnvironmentServiceClient, pgClient divergev1alpha1connect.PreviewGroupServiceClient, allowDestructive bool) *server.MCPServer {
	registry := mcpruntime.NewToolRegistry()

	envHandler := &mcpEnvHandler{client: envClient}
	divergev1alpha1.RegisterEnvironmentServiceMCP(registry, envHandler, mcpruntime.WithToolNamer(divergeToolNamer))

	pgHandler := &mcpPgHandler{client: pgClient}
	divergev1alpha1.RegisterPreviewGroupServiceMCP(registry, pgHandler, mcpruntime.WithToolNamer(divergeToolNamer))

	registerWaitForReady(registry, envClient)
	registerFetchErrors(registry, envClient)

	mcpServer := server.NewMCPServer("diverge", "1.0.0")

	for _, def := range registry.Tools() {
		toolName := def.Name
		if service, method, ok := strings.Cut(def.Name, "_"); ok && !strings.HasPrefix(def.Name, "diverge_") {
			toolName = divergeToolNamer(service, method)
		}

		// Skip streaming methods (proto2mcp generated them because it couldn't skip them, but MCP tools are request-response)
		if strings.Contains(toolName, "watch") || strings.Contains(toolName, "stream_logs") {
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

func runMCP(ctx context.Context, app *App, serverURL string, allowDestructive bool) error {
	if serverURL == "" {
		serverURL = os.Getenv("DIVERGE_SERVER_URL")
		if serverURL == "" {
			return fmt.Errorf("server URL required: use --server-url or set DIVERGE_SERVER_URL")
		}
	}

	httpClient := http.DefaultClient
	envClient := divergev1alpha1connect.NewEnvironmentServiceClient(httpClient, serverURL)
	pgClient := divergev1alpha1connect.NewPreviewGroupServiceClient(httpClient, serverURL)

	mcpServer := newMCPServer(envClient, pgClient, allowDestructive)

	return server.ServeStdio(mcpServer)
}
