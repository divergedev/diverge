# Observability

Diverge provides built-in observability by exposing Prometheus metrics and standard health endpoints.

## Endpoints
- **Metrics**: Exposed at `:9090/metrics`
- **Health**: Exposed at `:9090/healthz`

## Prometheus Metrics
The Diverge server collects the following metrics under the `diverge_server` namespace/subsystem:
- `diverge_server_rpc_requests_total`: Total number of RPC requests by method and status.
- `diverge_server_rpc_request_duration_seconds`: RPC request duration in seconds.
- `diverge_server_rpc_stream_duration_seconds`: Duration of streaming RPCs in seconds.
- `diverge_server_rpc_active_streams`: Number of active streaming RPCs.
- `diverge_server_auth_attempts_total`: Authentication attempts by provider and result.
- `diverge_server_broadcaster_subscribers`: Number of active broadcaster subscribers.

## ServiceMonitor Setup
Diverge can be configured to automatically create a `ServiceMonitor` object by enabling it in the Helm values. This allows Prometheus Operator to automatically discover and scrape the metrics endpoint.

## Dashboards & Alerts
- **Grafana Dashboard**: Import the reference dashboard provided in the `deploy/grafana` directory to visualize the metrics above.
- **Alerting Rules**: Pre-configured Prometheus alerting rules can be deployed alongside the ServiceMonitor to alert on high RPC error rates or anomalous stream durations.
