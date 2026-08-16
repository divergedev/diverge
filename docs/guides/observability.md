# Observability

Diverge provides built-in observability by exposing Prometheus metrics and standard health endpoints.

## Endpoints
- **Metrics**: Exposed at `:9090/metrics`
- **Liveness**: Exposed at `:9090/healthz` (always returns 200)

The controller uses standard controller-runtime metrics at `:8080/metrics` (workqueue depth, reconcile duration, etc).

In addition, Diverge exposes custom controller metrics under the `diverge_controller` namespace:
- `diverge_controller_async_provisions_total` (labels: protocol, result) — Total async route provisions
- `diverge_controller_async_provision_duration_seconds` (labels: protocol) — Async route provisioning duration
- `diverge_controller_async_teardowns_total` (labels: protocol, result) — Total async route teardowns
- `diverge_controller_async_active_routes` (labels: protocol) — Currently active async routes

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
Diverge can be configured to automatically create `ServiceMonitor` objects by enabling them in the Helm values (`.Values.metrics.serviceMonitor.*` for the controller, and `.Values.server.metrics.serviceMonitor.*` for the server). This allows Prometheus Operator to automatically discover and scrape the metrics endpoints.

## Dashboards & Alerts
- **Grafana Dashboard**: Import the reference dashboard provided in the `deploy/grafana` directory to visualize the metrics above.
- **Alerting Rules**: Pre-configured Prometheus alerting rules can be deployed alongside the ServiceMonitor to alert on high RPC error rates or anomalous stream durations.
