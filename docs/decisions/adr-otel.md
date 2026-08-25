# ADR: OpenTelemetry Auto-Instrumentation

## Status
Accepted

## Context
We need a way to automatically trace requests flowing through Diverge preview environments and differentiate them from baseline traffic.

## Decisions

### 1. Why init-containers not sidecars
We rely on the OpenTelemetry Operator's init-container injection mechanism rather than long-running sidecars for non-Go auto-instrumentation agents. This ensures compatibility with Knative (which scales to zero and has strict lifecycle expectations) and Istio Ambient mesh (which handles L4/L7 concerns outside the pod). Sidecars would complicate the pod lifecycle and resource accounting. Note that Go auto-instrumentation uses eBPF which inherently requires a privileged sidecar instead of an init-container.

### 2. Why W3C Baggage for x-preview-* propagation
To maintain context across boundaries, we inject preview identifiers into W3C Baggage instead of relying solely on custom HTTP headers (`x-preview-env`). Baggage is natively propagated by all OTel auto-instrumentation agents across transports with configured OTel carriers. Note that Kafka/Temporal support requires protocol-specific instrumentation to correctly parse and propagate the baggage.

### 3. Why no bundled collector
We do not bundle an OpenTelemetry Collector with Diverge. Users have diverse observability stacks (Datadog, Honeycomb, Grafana Tempo) and usually have a pre-existing collector or agent in their cluster. Bundling one would increase resource footprint and maintenance burden.

### 4. Why Go eBPF disabled by default
The Go auto-instrumentation provided by the OTel Operator uses eBPF, which requires `CAP_SYS_PTRACE` privileges. Granting elevated privileges to application pods in a preview environment poses a security risk. Therefore, it is disabled by default. Users are encouraged to manually instrument Go services or explicitly enable eBPF if the security tradeoff is acceptable.
