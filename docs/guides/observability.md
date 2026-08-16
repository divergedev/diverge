# Operator Observability Guide

Effective monitoring is crucial for operating Diverge in a production Kubernetes cluster. Diverge is built with observability as a first-class citizen, providing deep insights into controller health, routing events, and environment lifecycles.

## Prometheus Metrics

The Diverge controller and its components expose Prometheus metrics on port `:9090` at two primary endpoints:
- `/metrics`: The Prometheus exposition endpoint.
- `/healthz`: Liveness and readiness probes.

### Available Metrics

Diverge exposes the following key metrics to help you monitor the system:

- `rpc_requests_total`: Total number of internal RPC requests made by the controller to its components.
- `rpc_request_duration_seconds`: Histogram of RPC request latencies, helping to identify bottlenecks in the provisioner or routing engine.
- `rpc_active_streams`: Number of currently active streaming connections (useful for monitoring CLI log streaming and `diverge dev` syncs).
- `rpc_panics_total`: Counter for caught panics in RPC handlers. An increase in this metric warrants immediate investigation.
- `auth_attempts_total`: Total authentication attempts, broken down by success and failure statuses.

## ServiceMonitor Setup (Helm)

If you are using the Prometheus Operator, you can easily scrape Diverge metrics by enabling the `ServiceMonitor` in your Helm values:

```yaml
metrics:
  enabled: true
  serviceMonitor:
    enabled: true
    interval: 30s
    labels:
      release: prometheus
```

This will automatically configure Prometheus to scrape the `:9090/metrics` endpoint of the Diverge controller.

## Grafana Dashboard

Diverge provides a pre-built Grafana dashboard for visualizing cluster health and preview environment usage.

To install the dashboard:
1. Navigate to the `deploy/grafana/` directory in the Diverge repository.
2. Import `diverge-operator-dashboard.json` into your Grafana instance.

The dashboard includes panels for:
- Active Environments over time
- Controller Reconciliation Latency
- RPC Request Rates and Errors
- Async Provisioning Queue Depths

## Alerting Rules

We recommend setting up the following Prometheus Alertmanager rules to ensure reliable operation:

### 1. Environment Stuck in Deploying

Alerts when an environment fails to reach the `Running` state after a reasonable time.

```yaml
groups:
- name: diverge.alerts
  rules:
  - alert: DivergeEnvironmentStuck
    expr: diverge_environment_state{state="Deploying"} > 0 and time() - diverge_environment_creation_time_seconds > 600
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "Preview Environment stuck in Deploying"
      description: "Environment has been provisioning for over 10 minutes. Check the Async Provisioner and Database logs."
```

### 2. Controller Error Rate

Alerts when RPC requests or reconciliations are failing.

```yaml
  - alert: DivergeHighErrorRate
    expr: rate(rpc_requests_total{status="error"}[5m]) / rate(rpc_requests_total[5m]) > 0.05
    for: 2m
    labels:
      severity: critical
    annotations:
      summary: "High RPC Error Rate in Diverge Controller"
      description: "More than 5% of internal RPC requests are failing."
```

### 3. TTL Expiry Failure

Alerts when environments have outlived their TTL but have not been successfully terminated.

```yaml
  - alert: DivergeGarbageCollectionStalled
    expr: diverge_environments_past_ttl_total > 0
    for: 15m
    labels:
      severity: warning
    annotations:
      summary: "Diverge GC is stalled"
      description: "Preview environments past their TTL are not being cleaned up successfully."
```
