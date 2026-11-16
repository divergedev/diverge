# Observability

Diverge provides built-in observability by exposing Prometheus metrics and standard health endpoints.

## Controller Metrics

The controller uses standard controller-runtime metrics at `:8080/metrics` (workqueue depth, reconcile duration, etc).

In addition, Diverge exposes custom controller and environment FinOps metrics:
- `diverge_environment_estimated_hourly_cost_usd` (labels: `environment`, `namespace`) — Estimated hourly cloud cost (USD) for each preview environment based on service counts, explicit `spec.serviceConfig.resources.cpuRequest` / `memoryRequest` specifications (falling back to `0.25` vCPU + `0.5` GiB = `$0.012/hr` per service when omitted), and database provisioning mode:
  - **Configurable Compute Rate Card**: Defaults to `$0.04/vCPU-hr` (`DIVERGE_COST_VCPU_HOUR_USD`) and `$0.004/GiB-hr` (`DIVERGE_COST_MEMORY_GIB_HOUR_USD`), dropping compute cost to `0` when annotated with `divergedev.com/hibernated: "true"` or when the environment phase is hibernated (total hourly cost also reaches `0` in terminal `Failed`, `Terminating`, or `Expired` phases).
  - **Idle Hibernation & Sleep/Wake History**: Setting `divergedev.com/hibernate-after: "30m"` (or relying on the 30m default; set `"0"` or `"off"` to disable) automatically marks idle environments `divergedev.com/hibernated: "true"`, records `divergedev.com/hibernated-at` and cumulative `divergedev.com/hibernated-seconds` across wake cycles, emits a `Hibernated` Kubernetes Event, and zeroes active compute cost while retaining isolated database charges.
  - **Configurable Database Rate Card**: Defaults to `$0.08/hr` (`DIVERGE_COST_DB_DEDICATED_HOUR_USD`) for isolated database modes (`cnpg`, `neon`, `snapshot`, `fresh`), `$0.01/hr` (`DIVERGE_COST_DB_SCHEMA_HOUR_USD`) for `schema` mode, and `$0.00/hr` for `shared`/`noop`/`none`.
- `diverge_environment_hibernation_savings_hourly_usd` (labels: `environment`, `namespace`) — Estimated hourly compute USD currently saved while an environment is hibernated.
- `diverge_environment_cpu_cores_requested` (labels: `environment`, `namespace`) — Total active CPU core requests attributed to preview workloads in each environment (`0` while hibernated).
- `diverge_environment_memory_gib_requested` (labels: `environment`, `namespace`) — Total active memory GiB requests attributed to preview workloads in each environment (`0` while hibernated).
- `diverge_environment_lifetime_seconds` (labels: `namespace`, `database_mode`) — Histogram of total preview environment lifetimes observed upon finalizer teardown.
- `diverge_environment_ttl_remaining_seconds` (labels: `environment`, `namespace`) — Seconds remaining until TTL expiry (exports negative values when an environment is stuck past `ExpiresAt` so `DivergeEnvironmentStuck` alerts fire accurately; environments stuck in `Terminating` for more than 15 minutes or annotated with `divergedev.com/force-teardown: "true"` bypass external finalizer blockers and emit a `ForceTeardown` Warning event).
- **End-to-End W3C Trace Context (`divergedev.com/traceparent`)**: GitHub and GitLab webhook handlers automatically extract incoming W3C `traceparent` HTTP headers (or mint a fresh `00-<trace-id>-<span-id>-01` trace context when absent) and attach `divergedev.com/traceparent` to created or updated `PreviewGroup`, `Environment`, and `AgentTask` CRs. `PreviewGroupReconciler` copies it to child `Environments`, `EnvironmentReconciler` links its OpenTelemetry reconcile span (`environment.name`, `environment.namespace`) to the caller's trace, and database migration and hook `Job` containers receive it as `TRACEPARENT`.
- `diverge_controller_async_provisions_total` (labels: `protocol`, `result`) — Total async route provisions
- `diverge_controller_async_provision_duration_seconds` (labels: `protocol`) — Async route provisioning duration
- `diverge_controller_async_teardowns_total` (labels: `protocol`, `result`) — Total async route teardowns
- `diverge_controller_async_active_routes` (labels: `protocol`) — Currently active async routes

## Server Metrics

Under `diverge_server` namespace:
- `diverge_server_rpc_requests_total` (labels: method, status) — Total RPC requests
- `diverge_server_rpc_request_duration_seconds` (labels: method) — RPC latency histogram
- `diverge_server_rpc_stream_duration_seconds` (labels: method) — Stream duration
- `diverge_server_rpc_active_streams` (labels: method) — Active stream gauge
- `diverge_server_auth_attempts_total` (labels: provider, result) — Auth attempts
- `diverge_server_broadcaster_subscribers` — Active SSE/stream subscribers
- `diverge_server_broadcaster_events_total` — Events published
- `diverge_server_broadcaster_drops_total` — Events dropped (slow consumers)

## Audit Logging

The Diverge ConnectRPC API Server includes structured audit logging for security events (authentication, authorization, and resource mutations).

All audit events are emitted as JSON lines via `slog` under the `audit` component. These logs can be easily ingested by standard log aggregators (e.g., FluentBit, Datadog, ELK).

Fields included in audit events:
- `component`: `"audit"`
- `event`: e.g., `"auth.success"`, `"auth.failure"`, `"authz.denied"`, `"resource.mutation"`
- `source_ip`: Extracted from the `X-Forwarded-For` or remote address.
- `path`: The RPC endpoint path.
- `user`: (If authenticated) The username extracted from the K8s TokenReview.
- `groups`: (If authenticated) The groups associated with the user.
- Action-specific attributes: resource kind, namespace, name, or authorization decision context.

## ServiceMonitor Setup

Diverge can be configured to automatically create `ServiceMonitor` objects to allow Prometheus Operator to discover and scrape metrics:
- Controller: `.Values.metrics.serviceMonitor.*`
- Server: `.Values.server.metrics.serviceMonitor.*`

## Grafana Dashboard

Import the reference dashboard provided in the `deploy/grafana/` directory to visualize the metrics above.

## Alerting Rules

Reference the pre-configured Prometheus alerting rules in the `deploy/prometheus/` directory to alert on high RPC error rates or anomalous stream durations.

## Health Endpoints

- `:9090/healthz` — Liveness probe (always returns 200, no dependency checks)
- `:8080/healthz` — Controller liveness
- `:8081/readyz` — Controller readiness
