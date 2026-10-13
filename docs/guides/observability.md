# Observability

Diverge provides built-in observability by exposing Prometheus metrics and standard health endpoints.

## Controller Metrics

The controller uses standard controller-runtime metrics at `:8080/metrics` (workqueue depth, reconcile duration, etc).

In addition, Diverge exposes custom controller and environment metrics:
- `diverge_environment_estimated_hourly_cost_usd` (labels: `environment`, `namespace`) — Estimated hourly cloud cost (USD) for each preview environment based on active replica counts, explicit `cpuRequest` / `memoryRequest` resource specifications (falling back to `$0.025/hr` per default pod when omitted), and database provisioning mode:
  - **Compute Rate Card**: Scales with `spec.deploy.resources.cpuRequest` (`$0.031611/vCPU-hr`) and `memoryRequest` (`$0.004237/GiB-hr`), or `$0.025/hr` per default pod (`0` when scaled to zero or annotated with `divergedev.com/hibernated: "true"`).
  - **Idle Hibernation**: Setting `divergedev.com/hibernate-after: "30m"` automatically marks idle environments `divergedev.com/hibernated: "true"`, emits a `Hibernated` Kubernetes Event, and zeroes active compute cost.
  - **Database Rate Card**: `$0.05/hr` for dedicated cluster/branch modes (`cnpg`, `neon`), `$0.02/hr` for `snapshot`/`fresh` modes, `$0.005/hr` for `schema` mode, and `$0.00/hr` for `shared`/`noop`.
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
