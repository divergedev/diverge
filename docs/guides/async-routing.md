# Async Routing Developer Guide

Diverge extends the concept of preview environments beyond synchronous HTTP/gRPC requests, bringing it into the asynchronous messaging world. This guide covers how Diverge handles Temporal workflows and Kafka consumers in preview environments.

## What is Async Routing?

In a standard microservice architecture, changes to a service might involve both synchronous API updates and asynchronous processing changes (e.g., a Temporal workflow or a Kafka consumer).

When you deploy a delta preview environment for a branch, you want the async messages originating from that preview to be processed by the preview instances, without affecting the baseline environment or having baseline workers pick up the preview tasks.

Diverge solves this by:
- Creating separate **Temporal Task Queues** for the preview environment.
- Creating isolated **Kafka Consumer Groups** (and topics if needed) for the preview environment.
- Using **Context Propagation** to pass the routing header (`x-preview-env`) across asynchronous boundaries.

## `diverge dev` and Async Provisioning

When you run `diverge dev`, the CLI communicates with the cluster to stand up your environment. Diverge blocks the readiness of the environment until async provisioning is fully `Ready`. This means your application won't start receiving traffic or messages until all necessary Temporal task queues and Kafka topics have been provisioned by the Async Provisioner.

## Injected Environment Variables

To make your application aware of the preview resources, Diverge automatically injects several environment variables into your preview containers:

- `TEMPORAL_TASK_QUEUE`: Set to the isolated task queue name (e.g., `baseline-queue-mr-123`).
- `KAFKA_CONSUMER_GROUP`: Set to the isolated consumer group.
- `DIVERGE_ENV_ID`: The unique identifier of the preview environment (e.g., `mr-123`).
- `DIVERGE_HEADER_KEY`: The header key used for routing (defaults to `x-preview-env`).

## SDK Integration (Go)

Diverge provides a Go SDK to easily integrate your application with the async routing logic.

### Temporal Propagator

To ensure that the preview context is passed from HTTP requests down to Temporal workflows, you must register the Diverge Temporal Propagator:

```go
import (
    "go.temporal.io/sdk/client"
    "go.temporal.io/sdk/worker"
    divergesdk "github.com/divergedev/diverge/sdk/go"
)

// Register the propagator in your Temporal client options
c, err := client.Dial(client.Options{
    ContextPropagators: []client.ContextPropagator{
        divergesdk.NewTemporalPropagator(),
    },
})
```

### Kafka Header Injection

For Kafka, the SDK provides wrappers or interceptors to inject the routing context into Kafka record headers.

```go
import divergesdk "github.com/divergedev/diverge/sdk/go"

// When producing a message, inject the context
headers := divergesdk.InjectKafkaHeaders(ctx)
```

## Header Customization

If your organization uses a different header for preview routing (e.g., `x-my-org-preview`), you can customize this behavior. The header key is injected as `DIVERGE_HEADER_KEY`.

When initializing the SDK, you can override the default:

```go
prop := divergesdk.NewTemporalPropagator(
    divergesdk.WithHeaderKey("x-my-org-preview"),
)
```

## Example Workflow Code

Here is how you might read the Diverge environment context within a Temporal workflow:

```go
import (
    "go.temporal.io/sdk/workflow"
    divergesdk "github.com/divergedev/diverge/sdk/go"
)

func MyWorkflow(ctx workflow.Context, arg string) (string, error) {
    // Get the current Diverge environment from context
    envID := divergesdk.GetEnv(ctx)

    if envID != "" {
        workflow.GetLogger(ctx).Info("Running in preview environment", "env", envID)
    }

    // ... workflow logic ...
    return "success", nil
}
```
