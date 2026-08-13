# Async Routing Architecture

## The Problem
Synchronous header propagation (via HTTP/gRPC) is straightforward, but preview environments frequently break in event-driven or async boundaries (e.g., message queues, cron jobs, webhooks, or email callbacks). When a request is queued and later processed by a worker, the `x-preview-env` header context is lost unless explicitly serialized into the message envelope.

## Patterns for Async Propagation

### Pattern 1: CloudEvents `divergecontext` Extension
For systems utilizing the [CloudEvents](https://cloudevents.io/) specification, leverage extension attributes.
- **Implementation:** Inject `divergepreviewenv` as an extension string.
- **Pros:** Standards-compliant, natively supported by many event routers.
- **Cons:** Requires services to understand CloudEvent schemas.

### Pattern 2: Temporal Workflow Context Propagation
For Temporal or Cadence workflows:
- **Implementation:** Register `Search Attributes` globally in your Temporal cluster, and use `ContextPropagators` in your SDK workers to map the HTTP header to workflow headers.
- **Pros:** Works seamlessly across workflow retries, child workflows, and activities.
- **Cons:** Limited to Temporal architectures.

### Pattern 3: Kafka / NATS Header Forwarding
Modern brokers support native message headers.
- **Implementation:** Middleware on your producers extracts `x-preview-env` and attaches it as a Kafka Record Header. Consumers intercept and inject it back into local thread context.
- **Pros:** Zero payload modification required.
- **Cons:** Legacy systems or older broker clients might strip headers.

### Pattern 4: Query-Param + Cookie Middleware (The v1 Solution)
When external systems (like third-party webhooks) call back into your system, they usually won't set custom headers.
- **Implementation:** Append `?preview_env=<env>` to callback URLs. An API gateway or BFF intercepts the query parameter and converts it back into the standard `x-preview-env` header.

## Decision Matrix

| Scenario | Recommended Pattern |
|----------|---------------------|
| Internal Pub/Sub (Kafka/RabbitMQ) | Pattern 3: Broker Header Forwarding |
| Event Mesh (Knative) | Pattern 1: CloudEvents Extension |
| Long-running Orchestration | Pattern 2: Temporal Search Attributes |
| 3rd-Party Webhooks (Stripe, Twilio)| Pattern 4: Query-Param Injection |

## Reference
*For further reading on how large-scale engineering teams solve async preview environments, see Shopify's approach to routing preview contexts through Resque and Kafka.*
