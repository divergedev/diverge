---
name: diverge
description: Build, verify, debug, and benchmark Kubernetes preview environments and autonomous AgentTasks with Diverge (.diverge.yaml, diverge CLI, and Diverge MCP server).
---

# Diverge Agent Skill

Use this skill whenever working in a repository configured with Diverge (`.diverge.yaml` or `diverge.yaml`), managing Kubernetes `Environment`, `PreviewGroup`, or `AgentTask` custom resources, or verifying changes against ephemeral preview environments.

---

## 1. Configuration & Pre-Flight Validation (`.diverge.yaml`)

1. **Scaffold or Inspect Configuration**:
   - To generate a starter `.diverge.yaml` from repository layout (`services/`, `apps/`, `charts/`):
     ```bash
     diverge init --config
     ```
2. **Validate `.diverge.yaml` Schema & Semantics**:
   - Always run `diverge validate` after editing `.diverge.yaml`:
     ```bash
     diverge validate -c .diverge.yaml
     ```
   - Verify that `defaults.routing.header_key` uses `x-diverge-env` (unless your mesh/gateway specifies a custom header) and `defaults.deploy.mode` is `delta` for minimal-footprint preview deployments.
3. **Detect Affected Services & Topology Impact**:
   - Before deploying a preview environment, determine which services changed relative to the base branch and inspect upstream ingress paths:
     ```bash
     diverge diff --base main
     diverge diff --base main --output json
     ```

---

## 2. Creating, Testing & Comparing Preview Environments

1. **Check Active Environments & Preview Groups**:
   ```bash
   diverge status
   diverge status -o json
   ```
2. **Send Requests & Compare Against Baseline (`diverge curl`)**:
   - `diverge curl` automatically resolves the environment's URL and injects `<headerKey>: <headerValue>` plus `baggage: diverge.env=<headerValue>`:
     ```bash
     # Query a preview environment endpoint
     diverge curl pr-42 /api/v1/health

     # Compare baseline vs preview response side-by-side (status, latency, body diff)
     diverge curl pr-42 /api/v1/checkout --compare-baseline

     # Send a POST payload to a preview environment
     diverge curl pr-42 /api/v1/orders -X POST -d '{"sku":"item-1"}'
     ```
3. **Open in Browser or Print Magic Links (`diverge open`)**:
   - Open or print (`-p, --no-browser`) the cookie-setting magic link (`?_diverge_env=<env>`) for an `Environment` or `PreviewGroup` so browser sessions route through `diverge-proxy` with the collapsible in-browser Floating DevToolbar:
     ```bash
     diverge open --magic pr-42
     diverge open --magic --no-browser pr-42
     ```
   - To clear the preview cookie and return to baseline in a browser, visit `?_diverge_env=clear` or `?_diverge_env=reset`.
   - Automated scrapers or tests that need un-modified HTML from `diverge-proxy` can opt out of DevToolbar script injection by sending `X-Diverge-Toolbar: off`.

---

## 3. Diagnosing Failures (`diverge doctor` & Logs)

When a preview environment is stuck in `Deploying`, `Pending`, `Degraded`, or `Failed`:

1. **Run Automated Root-Cause Diagnostics**:
   ```bash
   diverge doctor pr-42 --json
   ```
   `diverge doctor` inspects pods, init containers, database branching/migration jobs, image pull status, and OOM/CrashLoop events with actionable remediation advice.
2. **Inspect Error Logs**:
   ```bash
   diverge logs pr-42
   ```

---

## 4. Diverge MCP Server Tools

When the Diverge MCP server (`diverge mcp`) is configured in the agent environment, prefer structured MCP tool calls over raw shell polling:

- `diverge_create_environment` / `diverge_create_preview_group`: Provision an isolated single-service or multi-service delta preview.
- `diverge_wait_for_ready`: Block until the environment reaches `Running`/`Ready` or `Failed` (up to 5 minutes) instead of polling `diverge_get_environment` in a loop.
- `diverge_doctor`: Run structured diagnostics on an environment (`{"name": "pr-42", "namespace": "default"}`).
- `diverge_fetch_errors`: Fetch truncated `ERROR`/`FATAL`/`PANIC` log lines without overflowing context windows.
- `diverge_loadtest`: Run a latency/RPS benchmark against an endpoint with `baseline_compare: true` to verify p50/p95/p99 regressions.
- `diverge_extend_ttl`: Extend the TTL of an active preview environment during debugging.

---

## 5. Dispatching & Steering Autonomous `AgentTask`s

Diverge supports spec-driven autonomous coding tasks (`AgentTask` CRD) running in isolated Kubernetes sandboxes:

```bash
# Scaffold a task spec
diverge task init

# Create and launch an AgentTask
diverge task create -f task.yaml

# Inspect status, token/USD budget, and PR URL
diverge task status <task-name>

# Send real-time steering guidance to a running task
diverge task guide <task-name> "Focus on fixing the migration rollback in services/api"

# Pause or resume execution
diverge task pause <task-name>
diverge task resume <task-name>
```

Corresponding MCP tools: `diverge_create_task`, `diverge_get_task`, `diverge_list_tasks`, `diverge_guide_task`, `diverge_pause_task`, `diverge_resume_task`.
