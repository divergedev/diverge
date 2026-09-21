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
