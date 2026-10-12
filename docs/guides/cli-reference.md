# CLI Reference

The `diverge` CLI manages your local development environments and interacts with the Diverge cluster controller.

## Commands

| Command | Description |
|---------|-------------|
| `diverge completion [bash\|zsh\|fish\|powershell]` | Generate shell completion scripts with live `Environment` and `AgentTask` tab-completion |
| `diverge create` | Create an environment from the current branch |
| `diverge delete <name>` | Delete an environment |
| `diverge dev` | Route cluster traffic for a service to your local machine |
| `diverge dev sessions` | List active developer sessions across services |
| `diverge dev intercept <service>` | Intercept a service in a preview group |
| `diverge dev release <service>` | Stop intercepting a service |
| `diverge diff` | Detect which services changed relative to a base branch |
| `diverge doctor [env-name]` | Run root-cause diagnostics (`DIV-101`–`DIV-110`) across workloads, migrations, and `AgentTask`s |
| `diverge env export` | Export environment variables for a service |
| `diverge graph show` | Display the service topology graph |
| `diverge graph validate` | Validate topology for cycles, orphans, and unreachable services |
| `diverge init` | Scaffold `.diverge.yaml` (`--config`) or bootstrap a local playground (`--provider auto\|kind\|k3d\|existing`) |
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
| `diverge validate` | Validate `.diverge.yaml` against JSON Schema or print the embedded schema (`--print-schema`) |
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
- `--server` — Diverge server address for the tunnel (default: auto-detect via port-forward)
- `--token` — Bearer token for the Diverge server (default: `$DIVERGE_TOKEN`, then the kubeconfig credential)
- `--on-conflict` — Conflict resolution policy: `warn` (default), `block`, or `allow` (Diverge Pro)
- `--force` — Force start dev session, overriding any active collision

The server authenticates tunnel connections by Kubernetes TokenReview, so the
token must be accepted for the server's `--audiences` (default:
`diverge-server`). The kubeconfig fallback works where that credential is such
a token; a provider-issued kubeconfig credential (GKE, EKS) needs an explicit
`--token`:

```bash
kubectl create token my-sa --audience diverge-server
```

### `diverge dev sessions`

List all active developer sessions and heartbeat leases across services in the cluster.

**Usage:** `diverge dev sessions [flags]` (Alias: `diverge dev list`)

**Flags:**
- `-n, --namespace` — Kubernetes namespace (default: from kubeconfig)

For full details on multi-user development, see the [Multi-User Dev Guide](multi-user-dev.md).

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

### `diverge init`

Initialize a `.diverge.yaml` repository configuration file or bootstrap a complete local Kubernetes development playground (Gateway API CRDs, Envoy Gateway, Diverge CRDs + Operator, optional KEDA, and sample `echo-server`).

**Usage:** `diverge init [flags]`

**Flags:**
- `-c, --config` — Generate a starter `.diverge.yaml` configuration file in the current directory and exit (without creating a cluster)
- `--provider` — Cluster provider to bootstrap: `auto` (default: detects existing context, `k3d`, or `kind`), `kind`, `k3d`, or `existing` (installs Diverge into your active `kubectl` context)
- `--cluster-name` — Local Kind or k3d cluster name (default: `diverge-dev`)
- `--install-gateway` — Install Envoy Gateway and Gateway API CRDs (default: `true`)
- `--install-keda` — Install KEDA and KEDA HTTP Add-on for scale-to-zero (default: `false`)
- `--install-sample-app` — Deploy the `echo-server` baseline sample deployment (default: `true`)
- `--dry-run` — Print the bootstrap commands without executing them

**Examples:**
```bash
# Scaffold a starter .diverge.yaml in your repository
diverge init --config

# Bootstrap a local playground using Kind
diverge init --provider kind --cluster-name diverge-local

# Bootstrap using k3d (or auto-detect kind/k3d)
diverge init --provider auto

# Install Diverge into an existing Kubernetes cluster context
diverge init --provider existing
```

### `diverge validate`

Validate `.diverge.yaml` (local file or remote HTTP/HTTPS URL) against the embedded JSON Schema, or export the JSON Schema for IDE YAML language server integration.

**Usage:** `diverge validate [flags]`

**Flags:**
- `-c, --config` — Path or URL to configuration file (default: `.diverge.yaml`)
- `-o, --output` — Output format: `text` (default) or `json`
- `--print-schema` — Print the embedded `.diverge.yaml` JSON Schema (`diverge-config.schema.json`) to stdout and exit

**Examples:**
```bash
# Validate .diverge.yaml in current directory
diverge validate

# Export JSON Schema for IDE autocompletion (VS Code / JetBrains / Neovim yaml-language-server)
diverge validate --print-schema > .diverge.schema.json
```

### `diverge completion`

Generate shell autocompletion scripts for `bash`, `zsh`, `fish`, and `powershell`. Includes **dynamic cluster-aware tab-completion** that queries the active namespace for live `Environment` names (`diverge status <TAB>`, `diverge logs <TAB>`, `diverge open <TAB>`, `diverge delete <TAB>`, `diverge doctor <TAB>`) and `AgentTask` names (`diverge task <subcommand> <TAB>`).

**Usage:** `diverge completion [bash|zsh|fish|powershell]`

**Examples:**
```bash
# Zsh (load in current session or add to ~/.zshrc)
source <(diverge completion zsh)

# Bash
source <(diverge completion bash)

# Fish
diverge completion fish | source

# PowerShell
diverge completion powershell | Out-String | Invoke-Expression
```

### `diverge doctor`

Run automated root-cause diagnostics across `Environment` CRs, database migration jobs, `AgentTask` sandboxes, and Kubernetes pods. Emits structured diagnostic error codes (`DIV-101` through `DIV-110`) with direct links to the [Troubleshooting Playbook](troubleshooting.md#diagnostic-error-codes-div-101--div-110).

**Usage:** `diverge doctor [environment] [flags]`

**Flags:**
- `--json` — Output machine-readable JSON diagnostic report (including `code` and `doc_url` fields per issue)


