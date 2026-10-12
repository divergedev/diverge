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
