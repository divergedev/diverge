# Observability

Diverge provides built-in observability by exposing Prometheus metrics and standard health endpoints.

## Controller Metrics

The controller uses standard controller-runtime metrics at `:8080/metrics` (workqueue depth, reconcile duration, etc).

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
