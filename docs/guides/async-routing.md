# Async Routing

Async routing allows you to route non-HTTP workloads such as Kafka consumers and Temporal workflows to your preview environments.

## How it works
The `diverge dev` command provisions the requested async routing resources (e.g. creating a dedicated Kafka consumer group or a Temporal task queue) and polls `Environment.status.conditions` until the `AsyncRoutingReady` condition becomes true.

## Injected Environment Variables
Diverge automatically provisions and injects specific environment variables into your preview pods to support async routing:

### Temporal
- `TEMPORAL_TASK_QUEUE`: The provisioned task queue for your preview environment.
- `TEMPORAL_NAMESPACE`: The Temporal namespace.

### Kafka
- `KAFKA_TOPIC`: The isolated topic for preview messages.
- `KAFKA_CONSUMER_GROUP`: The preview consumer group.
- `KAFKA_BROKERS`: The Kafka brokers string.

These variables are merged into your pod environment. There are blocked environment variables that cannot be overridden by async routing: `KUBERNETES_SERVICE_HOST`, `KUBERNETES_SERVICE_PORT`, `HOME`, `PATH`, and `KUBECONFIG`.

## SDK Integration

The `pkg/sdk` module provides helpers to extract and propagate the preview environment context.

### Context
Use `sdk.GetEnv(ctx)` to retrieve the current environment from a context.

### Header Customization
By default, the SDK looks for the `x-preview-env` header. You can customize this by setting the `DIVERGE_HEADER_KEY` environment variable.

### Kafka Headers
To inject the preview environment context into outgoing Kafka messages, use:
```go
func InjectHeaders(headers []Header, envName string) []Header
```

### Temporal Propagator
Temporal contexts can be propagated to workflows using the Temporal propagator:
```go
func NewContextPropagator() *ContextPropagator
```
