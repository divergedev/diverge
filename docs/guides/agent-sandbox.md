# Agent Sandbox Guide

The Diverge Agent Sandbox provides a secure, multi-tenant execution environment for autonomous AI development agents within Kubernetes. It enables agents to autonomously build features, fix bugs, test changes against ephemeral preview environments, and submit draft pull requests under strict cryptographic and resource boundaries.

---

## Architecture Overview

```
┌────────────────────────────────────────────────────────────────────────┐
│                              DIVERGE CLI                               │
│        diverge task init | create | status | logs | pause | resume     │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ ConnectRPC / K8s Client
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                        AGENTTASK CONTROLLER                            │
│  • 4-Tier On/Off Controls   • Client.MergeFrom Status Patching         │
│  • Pluggable TokenStore     • Dynamic FeatureGate Gating               │
└───────────────────┬───────────────────────────────┬────────────────────┘
                    │                               │
       Mints Root Macaroon              Provisions Bound Pod
                    ▼                               ▼
┌───────────────────────────────────┐ ┌──────────────────────────────────┐
│             PKG/AUTH              │ │           PKG/SANDBOX            │
│  • HMAC-SHA256 Token Chaining     │ │  • Capability Interfaces         │
│  • Cryptographic Nonce Freshness  │ │  • Pluggable Provider Registry   │
│  • Fail-Closed Caveat Checking    │ │  • Ephemeral 10Gi Limit & Drop   │
└───────────────────────────────────┘ └──────────────────────────────────┘
```

---

## 1. Four-Tier On/Off Controls

Diverge provides four independent layers of operational control to enable, disable, or suspend agent sandboxing:

| Tier | Level | Mechanism | Operational Behavior |
| :--- | :--- | :--- | :--- |
| **Tier 1** | **Compile-Time** | `//go:build !no_sandbox` | Binary exclusion. Strips sandbox providers and controllers from the compiled binary. |
| **Tier 2** | **Deployment-Time** | Flag `--enable-agent-sandbox=false`<br>Helm `sandbox.enabled: false` | Gated in `main.go`. Reconciler registration and API watch informers are omitted, saving cluster memory. |
| **Tier 3** | **Dynamic Runtime** | `FeatureGate.IsEnabled(ctx, "agent_sandbox")` | Pauses active reconciliation at runtime with zero pod restarts. Terminating tasks always cleanly remove finalizers. |
| **Tier 4** | **Task Lifecycle** | `spec.suspended: true`<br>`diverge task pause / resume` | Pauses individual tasks, stops sandbox compute, and preserves accumulated work. |

---

## 2. Repository Configuration (`.diverge/agent.yaml`)

Repositories can declare their autonomous agent parameters using a `.diverge/agent.yaml` file:

```yaml
version: v1
agent:
  # Allowed LLM models for this repository
  allowedModels:
    - claude-3-5-sonnet
    - gpt-4o
    - gemini-2.5-pro

  # Allowed tools and commands within the sandbox
  allowedTools:
    - git
    - go
    - npm
    - pytest
    - diverge_*

  # Spend limits for token consumption and API usage
  budgetUSD: "5.00"

  # Maximum autonomous build-test-review loops
  maxIterations: 5

  # Base branch for agent work and draft PRs
  baseBranch: main

  # Sandbox execution configuration
  sandbox:
    provider: agent-sandbox
    timeoutSeconds: 3600
```

### Initializing Configuration

Run `diverge task init` inside your repository to scaffold the configuration:

```bash
diverge task init
# Or overwrite an existing configuration:
diverge task init --force
```

---

## 3. Cryptographic Macaroon Authentication

Every agent sandbox receives a short-lived, cryptographically bounded token mounted at `/etc/diverge/token` with mode `0400`.

- **Cryptographic Nonce Freshness**: Every token issuance generates a 128-bit cryptographic nonce caveat (`nonce = <hex>`). Even identical tasks running on the same branch receive distinct cryptographic signatures.
- **Fail-Closed Caveat Evaluation**: Unknown caveat keys or invalid operators immediately reject authorization.
- **In-Process Attenuation**: Sub-agents can derive further restricted child tokens (e.g. read-only tool sets or lower budget thresholds) without contacting the Diverge control plane.

---

## 4. Granular Capability Interfaces (`pkg/sandbox`)

Providers implement fine-grained capability interfaces adhering to the Interface Segregation Principle:

- **`SandboxAttacher`**: Interactive terminal streaming with `ResizeChan <-chan TerminalSize`.
- **`SandboxPortForwarder`**: Local port tunneling into the running sandbox pod.
- **`SandboxFileTransfer`**: Streaming tar archive uploads and downloads with backpressure handling.
- **`SandboxPoolManager`**: Management of pre-warmed sandbox pools for sub-second startup latency.
- **`SandboxMetricsCollector`**: Real-time cgroup v2 / PSI (Pressure Stall Information) metrics retrieval.
- **`SandboxSnapshotter`**: Checkpoint and restore capabilities for paused agent states.
- **`CapabilityDetector`**: Feature negotiation via `Supports(cap Capability) bool`.

---

## 5. Adding a Custom Sandbox Provider

You can add alternative sandbox runtimes (e.g., Docker, Firecracker, WASM) by registering them with `pkgsandbox.Providers`:

```go
package custom

import (
    "github.com/divergedev/diverge/pkg/registry"
    pkgsandbox "github.com/divergedev/diverge/pkg/sandbox"
)

func init() {
    pkgsandbox.Providers.Register("docker", registry.Provider[pkgsandbox.SandboxProvider]{
        Create: func(deps registry.Deps) (pkgsandbox.SandboxProvider, error) {
            return NewDockerSandboxProvider(deps.Logger), nil
        },
        Description: "Local Docker daemon sandbox provider",
    })
}
```

Once registered, pass `--sandbox-provider=docker` to the Diverge controller to use it.

---

## 6. CLI Command Reference

### Create Task
```bash
# Basic task submission
diverge task create "Implement user avatar upload with S3 storage"

# Specifying budget and base branch
diverge task create "Refactor database migrations" --budget 10.00 --base-branch develop

# Dry-run output formatted as YAML
diverge task create "Fix memory leak in caching layer" --dry-run -o yaml
```

### Monitor & Stream Logs
```bash
# Inspect task status and progress
diverge task status task-1718902842

# Stream live execution logs (exits with code 1 if task fails)
diverge task logs task-1718902842 -f
```

### Steering & Guidance
```bash
# Provide human-in-the-loop steering to an active agent
diverge task guide task-1718902842 "Ensure backward compatibility with API v1 clients"
```

### Pause & Resume
```bash
# Pause an active task
diverge task pause task-1718902842

# Resume with a budget bump
diverge task resume task-1718902842 --budget 15.00 --tokens 2000000
```

---

## 7. Live LLM Cost & Token Enforcement (`POST /api/v1/agenttask/meter`)

Sandbox agents and in-cluster LLM proxies report real-time model token and cost usage to `diverge-server` via `POST /api/v1/agenttask/meter`, authenticating with `Authorization: Bearer <macaroon-token>`.

### Request Payload
```json
{
  "model": "claude-3-7-sonnet",
  "tokensUsed": 1500,
  "costUSD": 0.0225
}
```

### Response Payload (`200 OK`)
```json
{
  "allowed": true,
  "suspended": false,
  "totalTokensUsed": 1500,
  "totalCostUSD": 0.0225
}
```

### Enforcement Behavior
- **Model Restriction (`403 Forbidden`)**: If the task's Macaroon token includes `allowed_models` caveats (`spec.capabilities`), omitting `model` or requesting a model outside `allowed_models` returns `403 Forbidden`.
- **Budget Exhaustion (`429 Too Many Requests`)**: If the updated `totalCostUSD` or `totalTokensUsed` exceeds the Macaroon caveats or `AgentTask` spec (`budgetUSD` / `budgetTokens`), `diverge-server` atomically transitions the `AgentTask` to `Paused`, sets `spec.suspended = true`, revokes the token in `diverge-agenttask-tokens`, and responds with `429 Too Many Requests` (`"suspended": true, "reason": "budget exceeded"`).

---

## 8. Automatic Linked Preview Environments

When `spec.repository.url` is set on an `AgentTask`, `diverge-controller` automatically provisions and owns a linked `Environment` named `agent-preview-<taskName>` so the agent can test its changes against a live preview URL.

### Input Annotations
- `divergedev.com/preview-enabled`: `"true"` or `"false"` (defaults to `"true"` when `spec.repository.url` is set).
- `divergedev.com/preview-service`: Target service name (defaults to `<task.name>`).
- `divergedev.com/preview-image`: Preview container image (defaults to `nginx:alpine` on port `80`).
- `divergedev.com/preview-branch`: Branch override (defaults to `spec.repository.workingBranch` or `baseBranch`).

### Output Annotations & Conditions
- **Output Annotations**: The controller populates `divergedev.com/preview-environment` and `divergedev.com/preview-url` on the `AgentTask`.
- **Status Condition**: Sets `PreviewEnvironmentReady` (`EnvironmentProvisioning` -> `EnvironmentRunning`).

---

## 9. PR / MR Slash Commands & Bot Interaction

You can interactively trigger, steer, inspect, and control `AgentTask` workflows directly from GitHub PR or GitLab MR comments:

- `/diverge guide <hint>` — Steers active agent execution with runtime guidance (injected as `DIVERGE_GUIDANCE` into the sandbox container).
- `/diverge resume [+$budget] [tokens]` — Resumes a paused `AgentTask` with optional additive budget bumps (e.g. `/diverge resume +$10.00 50000`).
- `/diverge budget [+$budget] [tokens]` — Adjusts budget caps directly without changing task suspension state.
- `/diverge pause` — Pauses the active `AgentTask` associated with the PR/MR (`spec.suspended = true`).
- `/diverge status` — Posts an immediate Markdown status table summarizing all active agent tasks and linked preview environments.
- `/diverge help` — Displays the interactive slash command reference table.
- `/diverge fix <objective>` — Spawns or updates an `AgentTask` for the PR/MR branch with the specified objective.
- `/diverge preview` — Provisions or updates the linked preview environment.
- `/diverge teardown` — Tears down preview environments for the PR/MR.
- `/diverge extend-ttl [duration]` — Extends the preview environment's TTL.

All commands post interactive Markdown confirmation replies back to the PR or MR comment thread.

---

## 10. Connecting `google/ax` & MCP Clients (`diverge mcp`)

Both sandboxed agents and external AI orchestrators (`google/ax`, Claude Code, Cursor, Gemini CLI) can manage and inspect `AgentTask` lifecycles via the Model Context Protocol (MCP) using `diverge mcp`:

```json
{
  "mcpServers": {
    "diverge": {
      "command": "diverge",
      "args": ["mcp"],
      "env": {
        "DIVERGE_SERVER_URL": "http://diverge-server.diverge-system.svc:8080",
        "DIVERGE_TOKEN_FILE": "/etc/diverge/token/token"
      }
    }
  }
}
```

### Exposed `AgentTask` MCP Tools
- `diverge_create_task` — Create and launch an autonomous `AgentTask` in an isolated sandbox.
- `diverge_get_task` — Retrieve specification, phase, sandbox status, and token/cost consumption.
- `diverge_list_tasks` — List `AgentTask` resources in a namespace.
- `diverge_pause_task` — Pause an active `AgentTask` and suspend its sandbox (`spec.operatingMode: Suspended`).
- `diverge_resume_task` — Resume a paused `AgentTask` with optional additive budget bumps (`budget_usd_bump`, `budget_tokens_bump`).
- `diverge_guide_task` — Send real-time steering instructions (`divergedev.com/guidance`) delivered to the agent on its next metering heartbeat.
- `diverge_delete_task` — Delete an `AgentTask` and tear down its sandbox (requires `--allow-destructive`).

### Production Helm Setup (`macaroonSecretRef`)
To enable cryptographic Macaroon minting and live budget metering in production, create a 32-byte shared secret and configure `sandbox.macaroonSecretRef` in Helm:

```bash
kubectl create secret generic diverge-macaroon-key \
  -n diverge-system \
  --from-literal=root-key="$(openssl rand -hex 32)"

helm upgrade --install diverge ./charts/diverge \
  -n diverge-system \
  --set sandbox.enabled=true \
  --set sandbox.macaroonSecretRef.name=diverge-macaroon-key \
  --set sandbox.macaroonSecretRef.key=root-key
```

