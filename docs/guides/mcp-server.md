# Model Context Protocol (MCP) Server Guide

The Diverge CLI includes a built-in Model Context Protocol (MCP) server that exposes Diverge preview environment management capabilities directly to AI assistants and IDEs (such as Claude Desktop, Claude Code, Cursor, and VS Code).

Using the MCP server, AI assistants can query active environments, provision new ephemeral previews, extend TTLs, monitor deployment readiness, and diagnose failure logs directly within your development workflow.

---

## 1. Overview

The `diverge mcp` command runs an MCP server over standard input/output (`stdio`) adhering to the [Model Context Protocol](https://modelcontextprotocol.io/) specification.

```text
+-------------------------------------------------------------+
|               AI Assistant / Agent / IDE                   |
|          (Claude Code, Claude Desktop, Cursor, VS Code)     |
+-------------------------------------------------------------+
                              |
                              | MCP (JSON-RPC over stdio)
                              v
+-------------------------------------------------------------+
|                      diverge mcp                            |
|             (stdio MCP Server / proto2mcp)                  |
+-------------------------------------------------------------+
                              |
                              | ConnectRPC (HTTP/1.1 / HTTP/2)
                              v
+-------------------------------------------------------------+
|                    diverge-server                           |
|             (Kubernetes API Facade / CRDs)                  |
+-------------------------------------------------------------+
```

### Tool Naming Convention

All MCP tools are named using the `diverge_<snake_case_method>` pattern derived from Diverge's protobuf service definitions:
- RPC `EnvironmentService.CreateEnvironment` &rarr; Tool `diverge_create_environment`
- RPC `EnvironmentService.GetEnvironment` &rarr; Tool `diverge_get_environment`
- RPC `PreviewGroupService.ListPreviewGroups` &rarr; Tool `diverge_list_preview_groups`

### Security Scoping

The MCP server enforces strict security boundaries:
- **Exposed Services**: Only `EnvironmentService` and `PreviewGroupService` operations are exposed.
- **Excluded Services**: Sensitive internal services such as `AuthService`, `ClusterService`, and `TunnelService` are **never** exposed to AI agents over MCP.
- **Safe by Default**: Destructive operations (`delete`) are disabled by default and require explicit user opt-in via `--allow-destructive`.

### Streaming RPC Handling

Standard MCP tool calls operate using request/response semantics. Streaming RPCs defined on the API server (`WatchEnvironments`, `StreamLogs`, `WatchPreviewGroups`) are automatically skipped during standard tool generation.

To provide streaming-like functionality without unbounded context consumption, Diverge registers dedicated non-streaming helper tools:
- `diverge_wait_for_ready`: Blocks until an environment reaches `Ready` or `Failed`/`Error` phase (up to 5 minutes), eliminating noisy polling loops.
- `diverge_fetch_errors`: Fetches recent `ERROR` and `FATAL` log lines from environment pods, protecting agent context windows from log spam.

---

## 2. Prerequisites

Before connecting your AI editor to the Diverge MCP server, ensure you have:

1. **Diverge CLI** installed (`diverge version`):
   ```bash
   curl -fsSL https://divergedev.com/install.sh | sh
   ```
2. **Access to a Diverge API Server** (`diverge-server`):
   - **In-cluster / Remote**: An accessible ConnectRPC endpoint (e.g., `https://diverge.example.com` or via ingress).
   - **Local Port-Forward**:
     ```bash
     kubectl port-forward -n diverge-system svc/diverge-server 8080:8443
     ```

---

## 3. Quick Start

Run the MCP server directly from your terminal to verify connectivity:

```bash
diverge mcp --server-url http://localhost:8080
```

You can also specify the server URL using the `DIVERGE_SERVER_URL` environment variable:

```bash
export DIVERGE_SERVER_URL="http://localhost:8080"
diverge mcp
```

The process will listen on `stdio` for JSON-RPC messages from MCP clients.

---

## 4. Tool Categories

The MCP server provides the following tools grouped by service:

### EnvironmentService Tools

| Tool Name | Description |
| :--- | :--- |
| `diverge_create_environment` | Provision a new preview environment with source branch, routing rules, and database configuration. |
| `diverge_get_environment` | Fetch details, conditions, URLs, and phase status of a specific preview environment. |
| `diverge_list_environments` | List environments with optional filters (namespace, branch, phase, label selector) and pagination. |
| `diverge_update_environment` | Update an existing environment spec or update mask. |
| `diverge_delete_environment` | Delete an environment and clean up its resources (*requires `--allow-destructive`*). |
| `diverge_extend_ttl` | Extend the remaining Time-To-Live (TTL) for an active preview environment. |
| `diverge_list_hook_jobs` | List database migration and post-deploy lifecycle hook Jobs for an environment. |
| `diverge_retry_hook` | Re-trigger a failed lifecycle hook Job (e.g., migration or post-deploy task). |

### PreviewGroupService Tools

| Tool Name | Description |
| :--- | :--- |
| `diverge_create_preview_group` | Create a multi-service PreviewGroup CR managing several microservices for an MR/PR. |
| `diverge_get_preview_group` | Retrieve status, member services, endpoints, and conditions for a PreviewGroup. |
| `diverge_list_preview_groups` | List PreviewGroups across namespaces with label selectors and pagination. |
| `diverge_update_preview_group` | Update configuration or service definitions for a PreviewGroup. |
| `diverge_delete_preview_group` | Tear down a PreviewGroup and its child environments (*requires `--allow-destructive`*). |

### Built-in Helper Tools

| Tool Name | Description |
| :--- | :--- |
| `diverge_wait_for_ready` | Block execution until a target environment reaches `Ready` or `Failed` phase (max 5 minutes). |
| `diverge_fetch_errors` | Retrieve the last error-level (`ERROR`, `FATAL`, `panic`) log lines from an environment to diagnose failures. |

---

## 5. Editor Setup

Configure your AI assistant or editor to launch `diverge mcp` as a stdio tool server.

### Claude Code / Claude Desktop

Add the server configuration to `~/.claude/claude_desktop_config.json` (or `~/.claude.json`):

```json
{
  "mcpServers": {
    "diverge": {
      "command": "diverge",
      "args": ["mcp", "--server-url", "http://localhost:8080"]
    }
  }
}
```

### Cursor

Create or edit `.cursor/mcp.json` in your workspace root or global Cursor settings:

```json
{
  "mcpServers": {
    "diverge": {
      "command": "diverge",
      "args": ["mcp", "--server-url", "http://localhost:8080"]
    }
  }
}
```

### VS Code

Add the server entry to `.vscode/mcp.json` in your project workspace:

```json
{
  "servers": {
    "diverge": {
      "type": "stdio",
      "command": "diverge",
      "args": ["mcp", "--server-url", "http://localhost:8080"]
    }
  }
}
```

> [!TIP]
> If your Diverge server is running behind a custom domain, set `DIVERGE_SERVER_URL` as an environment variable in your MCP configuration:
> ```json
> {
>   "mcpServers": {
>     "diverge": {
>       "command": "diverge",
>       "args": ["mcp"],
>       "env": {
>         "DIVERGE_SERVER_URL": "https://diverge.internal.example.com"
>       }
>     }
>   }
> }
> ```

---

## 6. Destructive Operations

By default, the Diverge MCP server operates in **safe mode**:
- Destructive tools (`diverge_delete_environment` and `diverge_delete_preview_group`) are **omitted** from the registered tool registry.
- AI agents cannot accidentally delete active preview environments or teardown running PreviewGroups.

### Enabling Destructive Tools

To allow your AI assistant to delete environments and preview groups, append the `--allow-destructive` flag:

```json
{
  "mcpServers": {
    "diverge": {
      "command": "diverge",
      "args": [
        "mcp",
        "--server-url", "http://localhost:8080",
        "--allow-destructive"
      ]
    }
  }
}
```

> [!WARNING]
> Enabling `--allow-destructive` allows the AI agent to initiate irreversible deletions and cleanup of Kubernetes preview namespaces and databases.

---

## 7. Flags Reference

### `diverge mcp` Command Flags

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--server-url` | `string` | `""` | Diverge API server URL. If empty, falls back to `DIVERGE_SERVER_URL` environment variable. |
| `--allow-destructive` | `bool` | `false` | Enable destructive tools (`delete` methods). |

### Global Flags

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--kubeconfig` | `string` | `~/.kube/config` | Path to the Kubernetes kubeconfig file. |
| `-n`, `--namespace` | `string` | `""` | Kubernetes namespace (defaults to active kubeconfig context). |
| `--context` | `string` | `""` | Kubernetes context name. |
| `--no-color` | `bool` | `false` | Disable ANSI color formatting. |

### Environment Variables

| Variable | Description |
| :--- | :--- |
| `DIVERGE_SERVER_URL` | URL of the Diverge API server (used when `--server-url` is omitted). |

---

## 8. Examples

Once configured, you can interact with Diverge naturally through your AI assistant:

### Inspecting Environments
- *"List all preview environments in the `staging` namespace"*
- *"Check the phase and status for environment `preview-mr-42`"*
- *"Show conditions and ingress endpoints for active environments"*

### Creating Previews
- *"Create a preview environment for my feature branch"*
- *"Provision a PreviewGroup for MR 88 containing the `payments-api` service"*

### Diagnosing Issues & Lifecycle
- *"Extend the TTL of environment `preview-mr-42` by 2 hours"*
- *"Wait for environment `preview-mr-42` to become ready, and if it fails, show me the error logs"*
- *"List all hook jobs for environment `preview-mr-42` and retry the database migration if failed"*
- *"Fetch the recent error logs for `payments-api` in the preview environment"*
