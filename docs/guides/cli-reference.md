# CLI Reference

The `diverge` CLI manages your local development environments and interacts with the Diverge cluster controller.

## Commands

| Command | Description |
|---------|-------------|
| `diverge create` | Create an environment from the current branch |
| `diverge delete <name>` | Delete an environment |
| `diverge dev` | Route cluster traffic for a service to your local machine |
| `diverge dev intercept <service>` | Intercept a service in a preview group |
| `diverge dev release <service>` | Stop intercepting a service |
| `diverge diff` | Detect which services changed relative to a base branch |
| `diverge env export` | Export environment variables for a service |
| `diverge graph show` | Display the service topology graph |
| `diverge graph validate` | Validate topology for cycles, orphans, and unreachable services |
| `diverge init` | Initialize a local development playground |
| `diverge list` | List all environments in the cluster |
| `diverge logs [env-name]` | Stream logs from a preview environment |
| `diverge mcp` | Run MCP server over stdio for AI agent integration |
| `diverge open <name>` | Open the environment URL in browser |
| `diverge plugins` | Manage plugins |
| `diverge preview create` | Create a preview group from current branch |
| `diverge preview status <name>` | Show status of a preview group |
| `diverge preview delete <name>` | Delete a preview group |
| `diverge preview watch <name>` | Watch a preview group until Ready/Failed |
| `diverge providers list` | List all registered providers |
| `diverge route <service>` | Simulate request routing to a service through the topology |
| `diverge status` | Show active preview environments and groups |
| `diverge validate` | Validate .diverge.yaml against JSON Schema |
| `diverge version` | Print version info |

## Detailed Command Usage

### `diverge dev`

Route cluster traffic for a service to your local machine.

**Flags:**
- `--service` — Service name (default: auto-detect)
- `--port` — Local port (default: 8080)
- `--endpoint` — Local endpoint IP (default: tailscale ip -4)
- `--env-output` — How to handle env vars: `inject` (in-memory) or `file` (.env.diverge)
- `--devspace` — Generate a devspace.yaml template
- `--proxy-port` — Port for the local proxy (default: 19001)
- `--no-proxy` — Disables the local proxy
- `--proxy-mode` — Proxy mode, either `path` or `host` (default: path)

### `diverge logs`

Stream logs from a preview environment.

**Flags:**
- `--service` — Filter to specific service
- `--follow` / `-f` — Follow log output
- `--tail` — Number of lines to show

### `diverge env export`

Export environment variables for a service.

**Supported output formats:**
- `dotenv` (default)
- `json`
- `shell`

### `diverge mcp`

Run a Model Context Protocol (MCP) server over `stdio` for AI agent integration (Claude Desktop, Cursor, VS Code).

**Flags:**
- `--server-url` — Diverge API server URL (defaults to `DIVERGE_SERVER_URL` env var)
- `--allow-destructive` — Enable destructive tools (`delete`)

See the [MCP Server Guide](mcp-server.md) for full setup instructions and editor configurations.

### `diverge diff`

Detect which services changed relative to a base branch.

**Flags:**
- `--config` — Path to `.diverge.yaml`
- `--base` — Base ref to compare against (default: `main`)
- `--output` — Output format: `text` (default), `json`

### `diverge route`

Simulate request routing to a service through the topology.

**Usage:** `diverge route <service> [flags]`

**Flags:**
- `--config` — Path to `.diverge.yaml`
- `--gateway` — Filter to specific gateway
- `--header` — Custom header name (default: `x-diverge-env`)
- `--output` — Output format: `text` (default), `mermaid`, `dot`, `json`
- `--live` — Use Prometheus for live topology

### `diverge graph show`

Display the service topology graph.

**Flags:**
- `--config` — Path to `.diverge.yaml`
- `--service` — Show ingress paths for a specific service
- `--gateway` — Filter to specific gateway
- `--output` — Output format: `text` (default), `mermaid`, `dot`, `json`

