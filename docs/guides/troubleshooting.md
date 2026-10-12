# Troubleshooting & `diverge doctor` Playbook

Use `diverge doctor` (or the `diverge_doctor` MCP tool) as your first step whenever an environment fails to reconcile.

```bash
diverge doctor
```

---

## Common Issues & Fixes

### 1. `Environment` Stuck in `Pending` or `Deploying`
**Symptoms**: `kubectl get env` shows `Phase: Deploying` for > 2 minutes.

**Diagnosis**:
```bash
diverge status <env-name>
diverge logs <env-name>
```
Or via MCP: invoke `diverge_fetch_errors` and `diverge_list_hook_jobs`.

**Common Causes**:
- **Database Migration Job Failed**: Run `diverge_list_hook_jobs` or `kubectl get jobs -n <namespace> -l divergedev.com/environment=<env-name>`. Fix the SQL migration or re-trigger via `diverge_retry_hook`.
- **ImagePullBackOff**: Verify your cluster nodes have pull credentials (`imagePullSecrets`) for your PR container image tag.

---

### 2. Requests Hit Baseline Production Instead of Preview Pod
**Symptoms**: Sending `curl -H "x-preview-env: pr-42"` returns responses from the baseline service.

**Diagnosis**:
```bash
diverge route <service-name>
```

**Common Causes**:
- **Header Name Mismatch**: Check that the `routing.headerKey` on your `PreviewGroup` / `Environment` (e.g., `x-preview-env` or `x-diverge-env`) matches the header sent by your client and propagated by intermediate services.
- **Downstream Service Dropped Header**: An upstream service called a downstream service without forwarding the preview header. See the [Header Propagation Starter Kits](../header-propagation.md).
- **Missing GatewayClass**: Run `kubectl get gatewayclass` and verify the `Gateway` referenced by `--gateway-name` is `Programmed: True`.

---

### 3. `AgentTask` Returns `401 Unauthenticated` When Calling `diverge-server`
**Symptoms**: Sandbox agent logs show `unauthenticated: token has been revoked` or `invalid macaroon signature`.

**Common Causes**:
- **Task Paused or Budget Exceeded**: Check `kubectl get agenttask <name> -o yaml`. If `status.phase` is `Paused` (due to `spec.suspended: true`, `maxIterations`, `budgetUSD`, or `budgetTokens`), increase the budget and set `spec.suspended: false` to mint a fresh token.
- **Mismatched `DIVERGE_MACAROON_ROOT_KEY`**: Ensure `sandbox.macaroonSecretRef.name` in Helm is mounted into both `diverge-controller` and `diverge-server`.

---

### 4. `Environment` or `PreviewGroup` Stuck in `Terminating`
**Symptoms**: `kubectl delete env <name>` hangs.

**Common Causes**:
- **Database Provider Teardown Blocked**: If the backing Postgres server is unreachable during schema drop, the controller retries teardown before removing `divergedev.com/finalizer`. Check controller logs:
  ```bash
  kubectl logs -n diverge-system deployment/diverge-controller-manager --tail=100
  ```

---

### 5. `POST /api/v1/agenttask/meter` Returns `429 Too Many Requests` or `403 Forbidden`
**Symptoms**: Sandbox agent or LLM proxy receives `429 Too Many Requests` (`{"allowed": false, "suspended": true, "reason": "budget exceeded"}`) or `403 Forbidden` (`{"allowed": false, "suspended": false, "reason": "model \"...\" is not allowed"}`).

**Common Causes & Fixes**:
- **`429 Too Many Requests` (`budget exceeded`)**: The `AgentTask`'s cumulative `totalCostUSD` or `totalTokensUsed` exceeded its Macaroon capability caveats or `spec.budgetUSD` / `spec.budgetTokens`. `diverge-server` automatically transitions the task to `Paused`, sets `spec.suspended: true`, and revokes its token in `diverge-agenttask-tokens`. To resume execution with a higher budget:
  ```bash
  diverge task resume <task-name> --budget 15.00 --tokens 500000
  # Or patch the AgentTask directly:
  kubectl patch agenttask <task-name> --type=merge -p '{"spec":{"suspended":false,"budgetUSD":"15.00","budgetTokens":500000}}'
  ```
- **`403 Forbidden` (`model not allowed`)**: The `AgentTask` restricts allowed LLM models via `spec.capabilities` (encoded as `allowed_models` caveats in the Macaroon token), and the metering request either omitted the `"model"` field or specified a model not listed in `spec.capabilities`. Add the model to `spec.capabilities` on the `AgentTask` (or `.diverge/agent.yaml`) so the controller mints a capability token permitting that model.

---

## Diagnostic Error Codes (`DIV-101` – `DIV-110`)

When `diverge doctor` (or `diverge doctor --json`) inspects a namespace, preview `Environment`, or `AgentTask`, every finding includes a stable diagnostic code (`DIV-101` through `DIV-110`) and a direct documentation link (`https://divergedev.com/guides/troubleshooting/#...`) to the corresponding section below.

| Code | Severity | Component | Summary |
| :--- | :--- | :--- | :--- |
| [`DIV-101`](#div-101-environment-failed) | `CRITICAL` | `Environment/<name>` | Environment phase is `Failed` or `Error` |
| [`DIV-102`](#div-102-migration-failed) | `CRITICAL` | `Environment/<name> (Migration)` | Database migration hook job failed |
| [`DIV-103`](#div-103-condition-false) | `WARNING` | `EnvironmentCondition/<type>` | Environment status condition is `False` |
| [`DIV-104`](#div-104-pod-unschedulable) | `CRITICAL` | `Pod/<name>` | Pod unschedulable (`PodScheduled: False`) |
| [`DIV-105`](#div-105-crashloopbackoff) | `CRITICAL` | `Container/<name>` | Container is repeatedly crashing (`CrashLoopBackOff`) |
| [`DIV-106`](#div-106-imagepullbackoff) | `CRITICAL` | `Container/<name>` | Container image pull failed (`ImagePullBackOff` / `ErrImagePull`) |
| [`DIV-107`](#div-107-container-config-error) | `CRITICAL` | `Container/<name>` | Missing `ConfigMap` or `Secret` (`CreateContainerConfigError`) |
| [`DIV-108`](#div-108-oomkilled) | `CRITICAL` / `WARNING` | `Container/<name>` | Container terminated due to Out Of Memory (`OOMKilled`) |
| [`DIV-109`](#div-109-container-exit) | `CRITICAL` / `WARNING` | `Container/<name>` | Container terminated with non-zero exit code |
| [`DIV-110`](#div-110-agenttask-budget-or-failure) | `CRITICAL` / `WARNING` | `AgentTask/<name>` | `AgentTask` failed or paused due to LLM budget/iteration limits |

### DIV-101: Environment Failed

- **Trigger**: The `Environment` Custom Resource has `status.phase` set to `Failed` or `Error`.
- **Common Causes**:
  - Deployment rollout exceeded its progress deadline because pods failed readiness/liveness probes.
  - Database schema provisioning (`CREATE SCHEMA`) or routing rule reconciliation (`HTTPRoute` / `GRPCRoute`) returned an unrecoverable error.
- **Remediation**:
  ```bash
  diverge status <env-name>
  diverge logs <env-name> --tail=100
  kubectl describe environment <env-name>
  ```

### DIV-102: Migration Failed

- **Trigger**: `status.migrationStatus` on the `Environment` CR is `Failed`.
- **Common Causes**:
  - A pre-deploy SQL migration script or `AtlasMigration` / `AtlasSchema` resource encountered a syntax error, lock timeout, or conflicting DDL statement.
  - The database credentials in `diverge-db-env-<env-name>` lack DDL privileges on the target database.
- **Remediation**:
  ```bash
  # Inspect the migration Job logs
  kubectl logs -l divergedev.com/environment=<env-name>,divergedev.com/hook=migration --tail=100
  # After fixing the migration or DB state, re-trigger via MCP tool `diverge_retry_hook` or re-apply the Environment
  ```

### DIV-103: Condition False

- **Trigger**: One or more entries in `status.conditions` on the `Environment` CR has `status: "False"` (for example, `DatabaseReady`, `DeploymentReady`, `RouteReady`, or `FeatureFlagsReady`).
- **Common Causes**:
  - Upstream infrastructure dependency (Gateway API controller, PostgreSQL instance, Flipt server, or Kafka/Temporal broker) is unreachable or misconfigured.
- **Remediation**:
  ```bash
  kubectl get environment <env-name> -o jsonpath='{.status.conditions}' | jq .
  ```
  Inspect the `reason` and `message` fields on the failing condition and verify the corresponding provider configuration.

### DIV-104: Pod Unschedulable

- **Trigger**: A preview pod is stuck in `Phase: Pending` with condition `PodScheduled: False`.
- **Common Causes**:
  - Insufficient cluster CPU or memory capacity (`Insufficient cpu` / `Insufficient memory`).
  - Node selector, node affinity, or taint/toleration mismatch (for example, requesting a `gvisor` or `kata` `RuntimeClass` on nodes without the runtime installed).
- **Remediation**:
  ```bash
  kubectl describe pod <pod-name>
  kubectl get nodes -o wide
  ```
  Lower resource `requests` in `.diverge.yaml`, enable **Scale-to-Zero** (`divergedev.com/hibernate-after: "30m"`) so idle PRs free node capacity, or scale up your node pool.

### DIV-105: CrashLoopBackOff

- **Trigger**: A container or init container is in `Waiting` state with `reason: CrashLoopBackOff`.
- **Common Causes**:
  - Application panicked on startup due to missing environment variables, invalid configuration, or failed database connection.
  - Liveness probe is failing too aggressively before the application finishes booting.
- **Remediation**:
  ```bash
  diverge logs <env-name> --service <service-name>
  kubectl logs <pod-name> --previous
  ```

### DIV-106: ImagePullBackOff

- **Trigger**: A container or init container is in `Waiting` state with `reason: ImagePullBackOff` or `ErrImagePull`.
- **Common Causes**:
  - The CI build has not yet pushed the PR image tag (e.g., `ghcr.io/org/service:pr-42`), or the image tag is misspelled.
  - The preview namespace is missing `imagePullSecrets` for your private container registry.
- **Remediation**:
  ```bash
  kubectl describe pod <pod-name>
  ```
  Confirm the image tag exists in your registry and ensure `imagePullSecrets` are configured on the service deployment or default `ServiceAccount`.

### DIV-107: Container Config Error

- **Trigger**: A container or init container is in `Waiting` state with `reason: CreateContainerConfigError`.
- **Common Causes**:
  - A `Secret` or `ConfigMap` referenced by `envFrom` or `env[].valueFrom` does not exist in the preview namespace, or a specific key is missing.
- **Remediation**:
  ```bash
  kubectl get secrets,configmaps -n <namespace>
  kubectl describe pod <pod-name>
  ```
  Verify that required secrets (such as `diverge-db-env-<env-name>` or application secrets) exist in the target namespace.

### DIV-108: OOMKilled

- **Trigger**: A container terminated (currently or in `lastState.terminated`) with `reason: OOMKilled` or an Out-Of-Memory kernel message.
- **Common Causes**:
  - Container memory usage exceeded `resources.limits.memory` during startup, asset compilation, or heavy query execution.
- **Remediation**:
  Increase the service's memory limit in `.diverge.yaml` or the `Environment` / `PreviewGroup` spec:
  ```yaml
  resources:
    requests:
      memory: "256Mi"
    limits:
      memory: "512Mi"
  ```

### DIV-109: Container Exit

- **Trigger**: A container or init container terminated with a non-zero exit code (`exitCode != 0`) for a reason other than `OOMKilled`.
- **Common Causes**:
  - Unhandled runtime exception, failed command entrypoint, or init container pre-check failure.
- **Remediation**:
  ```bash
  kubectl logs <pod-name> -c <container-name> --previous
  ```

### DIV-110: AgentTask Budget or Failure

- **Trigger**: An `AgentTask` Custom Resource is in `status.phase: Failed` or `status.phase: Paused` (for example, `Reason: BudgetExceeded`, `MaxIterationsReached`, or `Suspended`).
- **Common Causes**:
  - Live LLM metering (`POST /api/v1/agenttask/meter`) recorded `totalCostUSD` or `totalTokensUsed` exceeding `spec.budgetUSD`, `spec.budgetTokens`, or the Macaroon token's `max_cost_usd` / `max_tokens` caveats.
  - The agent exceeded `spec.maxIterations` or the sandbox job exited with an error.
- **Remediation**:
  ```bash
  kubectl get agenttask <task-name> -o yaml
  # Resume a paused task with an increased budget:
  diverge task resume <task-name> --budget 15.00 --tokens 500000
  ```
  Or comment `/diverge resume` on the pull request after updating the budget on the `AgentTask`.


