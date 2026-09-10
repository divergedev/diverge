# Diverge Doctor

`diverge doctor` runs automated root-cause diagnostics across your Kubernetes workloads, database migrations, and Diverge Environment CRDs. It detects common failure patterns and provides actionable remediation advice.

---

## Usage

```bash
# Diagnose all environments in the current namespace
diverge doctor

# Diagnose a specific preview environment
diverge doctor pr-42 -n default

# Machine-readable JSON output for CI pipelines and AI agents
diverge doctor pr-42 --json
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-n, --namespace` | Current context namespace | Kubernetes namespace to inspect |
| `--json` | `false` | Output structured JSON instead of a human-readable table |

---

## What It Checks

### Environment Health

| Check | Severity | Trigger |
|-------|----------|---------|
| **Phase Failed/Error** | CRITICAL | `env.Status.Phase` is `Failed` or `Error` |
| **Migration Failed** | CRITICAL | `env.Status.MigrationStatus` is `Failed` |
| **False Conditions** | WARNING | Any `env.Status.Conditions` entry with `Status: False` |

### Pod & Container Health

| Check | Severity | Trigger |
|-------|----------|---------|
| **Unschedulable Pod** | CRITICAL | `PodScheduled` condition is `False` (insufficient resources or node selector mismatch) |
| **CrashLoopBackOff** | CRITICAL | Container waiting with reason `CrashLoopBackOff` |
| **Image Pull Failure** | CRITICAL | Container waiting with reason `ImagePullBackOff` or `ErrImagePull` |
| **Config Error** | CRITICAL | Container waiting with reason `CreateContainerConfigError` (missing ConfigMap/Secret) |
| **OOMKilled** | CRITICAL | Container terminated with reason `OOMKilled` |
| **Non-Zero Exit** | CRITICAL | Container terminated with non-zero exit code |
| **Previous Crash** | WARNING | Container's `LastTerminationState` shows a prior crash or OOM |

---

## Output Formats

### Table (default)

```
$ diverge doctor pr-42 -n default

┌──────────┬──────────────────────────────────────┬──────────────────────────────────┬──────────────────────────────────────────────────┐
│ SEVERITY │ COMPONENT                            │ SUMMARY                          │ REMEDIATION                                      │
├──────────┼──────────────────────────────────────┼──────────────────────────────────┼──────────────────────────────────────────────────┤
│ CRITICAL │ Environment/pr-42                    │ Environment phase is Failed       │ Check container logs, health check probes, and   │
│          │                                      │                                  │ init container status.                            │
├──────────┼──────────────────────────────────────┼──────────────────────────────────┼──────────────────────────────────────────────────┤
│ CRITICAL │ Container/api (pod-abc12)            │ Container is repeatedly crashing  │ Inspect recent application logs with             │
│          │                                      │ (CrashLoopBackOff)               │ `diverge logs` or verify startup command.         │
└──────────┴──────────────────────────────────────┴──────────────────────────────────┴──────────────────────────────────────────────────┘

doctor detected 2 issue(s) requiring attention
```

### JSON (`--json`)

```json
{
  "environment_name": "pr-42",
  "namespace": "default",
  "healthy": false,
  "issues": [
    {
      "severity": "CRITICAL",
      "component": "Environment/pr-42",
      "summary": "Environment phase is Failed",
      "details": "DeployReady condition is False",
      "remediation": "Check container logs, health check probes, and init container status."
    }
  ],
  "suggestions": []
}
```

---

## CI Integration

Use `--json` output in CI pipelines to fail builds when environments are unhealthy:

```yaml
# GitHub Actions
- name: Health Check
  run: |
    diverge doctor ${{ env.PREVIEW_NAME }} --json > doctor-report.json
    # diverge doctor exits with code 1 when issues are detected
```

The command exits with code **0** when all checks pass and code **1** when any issues are detected.

---

## MCP Integration

`diverge doctor` is also available as an MCP tool (`diverge_doctor`) for AI-assisted debugging. When running `diverge mcp`, AI coding assistants can invoke diagnostics directly:

```json
{
  "name": "diverge_doctor",
  "arguments": {
    "namespace": "default",
    "environment": "pr-42"
  }
}
```

This enables AI agents to automatically diagnose environment failures and suggest fixes without manual intervention.

---

## Common Scenarios

### Database Migration Failure

```
CRITICAL │ Environment/pr-42 (Migration) │ Database migration hook failed
```

**Fix**: Inspect migration Job logs:
```bash
kubectl logs -n default job/atlas-pr-42-versioned-abc12 --tail=50
```

Check for SQL syntax errors, missing tables, or schema conflicts. See [Database Atlas Guide](database-atlas.md) for migration configuration.

### OOMKilled Container

```
CRITICAL │ Container/api (pod-xyz89) │ Container was terminated due to Out Of Memory (OOMKilled)
```

**Fix**: Increase memory limits in your `.diverge.yaml`:
```yaml
services:
  api:
    resources:
      limits:
        memory: 1Gi  # Increase from default
```

### Image Pull Failure

```
CRITICAL │ Container/api (pod-xyz89) │ Container image pull failed
```

**Fix**: Verify the image exists and your cluster has pull credentials:
```bash
# Check image exists
docker pull registry.example.com/api:pr-42

# Check imagePullSecrets
kubectl get sa default -n default -o jsonpath='{.imagePullSecrets}'
```
