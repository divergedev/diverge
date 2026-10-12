# Architecture

Diverge is built as a Kubernetes controller paired with a powerful CLI, ConnectRPC API server, L7 routing proxies, and SDKs for developers and autonomous AI agents.

![Diverge End-to-End Architecture](./assets/architecture-overview.svg)

## High-Level Architecture
1. **Controller**: The Diverge controller watches `PreviewGroup`, `Environment`, and `AgentTask` CRDs and reconciles them.
2. **Provisioner**: Database schemas (`preview_<env>`), feature flag rules, and async routing resources are provisioned on-the-fly (e.g. Kafka topics, Temporal queues).
3. **Status**: The controller updates the `Environment` and `PreviewGroup` status, reporting `AsyncRoutingReady` and `PreviewEnvironmentReady`.
4. **CLI & MCP**: The developer CLI (`diverge`) and MCP server interact with the cluster, blocking on ready states, tracing routes, and streaming logs.

## Delta Previews vs. Full-Stack Duplication

![Delta Previews vs. Full-Stack Duplication](./assets/delta-vs-duplicated.svg)

## Async Routing Flow

```mermaid
sequenceDiagram
    participant CLI as diverge dev
    participant Controller
    participant Async as Async Provisioner
    participant Env as Environment CRD
    participant Prometheus

    CLI->>Controller: Create/Update Environment
    Controller->>Async: Provision Async Route
    Async-->>Controller: Return EnvVars (e.g. KAFKA_TOPIC)
    Controller->>Env: Update Status.AsyncEnvVars & AsyncRoutingReady=True

    loop Poll until Ready
        CLI->>Env: Get Environment Status
        Env-->>CLI: Return Status (AsyncRoutingReady)
    end

    CLI->>CLI: Apply EnvVars locally

    Prometheus->>Controller: Scrape /metrics
    Controller-->>Prometheus: Return metrics
```

## SDK Propagation
The `pkg/sdk` ensures that the preview environment context is propagated correctly across system boundaries:
- **HTTP/gRPC**: Headers are injected and extracted using the key `x-preview-env` (or overridden via `DIVERGE_HEADER_KEY`).
- **Kafka**: Context is injected into message headers using `kafka.InjectHeaders()`.
- **Temporal**: Workflows receive context via the `ContextPropagator` implemented for Temporal.

## Metrics Collection
The controller and server expose metrics on `:9090` (`/metrics`). Prometheus scrapes this endpoint and visualizes the telemetry (e.g. `diverge_server_rpc_requests_total`) in Grafana.
