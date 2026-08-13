# Async Routing Architecture

## The Problem
Synchronous header propagation (via HTTP/gRPC) is straightforward, but preview environments frequently break in event-driven or async boundaries (e.g., message queues, cron jobs, webhooks, or email callbacks). When a request is queued and later processed by a worker, the `x-preview-env` header context is lost unless explicitly serialized into the message envelope.

## Patterns for Async Propagation

### Pattern 1: CloudEvents `divergepreviewenv` Extension
For systems utilizing the [CloudEvents](https://cloudevents.io/) specification, leverage extension attributes.
- **Implementation:** Inject `divergepreviewenv` as a CloudEvents extension string attribute on the producer side. Consumers read the extension and inject it as `x-preview-env` for downstream RPC calls.
- **Extension name:** `divergepreviewenv` (all lowercase, no hyphens — per CloudEvents naming rules)
- **Pros:** Standards-compliant, natively supported by many event routers.
- **Cons:** Requires services to understand CloudEvent schemas.

### Pattern 2: Temporal Workflow Context Propagation
For Temporal or Cadence workflows:
- **Implementation:** Use Temporal's `ContextPropagator` interface to serialize the `x-preview-env` HTTP header into Temporal workflow headers. Register the propagator in your SDK worker options. The propagator automatically carries the value across workflow starts, activity invocations, child workflows, and retries.
- **Search Attributes (optional):** Register a `diverge_preview_env` Search Attribute for visibility and querying (e.g., "show me all workflows running in preview `mr-42`"). Search Attributes are **not** a propagation mechanism — they provide indexed metadata for queries only.
- **Pros:** Works seamlessly across workflow retries, child workflows, and activities.
- **Cons:** Limited to Temporal architectures.

### Pattern 3: Kafka / NATS Header Forwarding
Modern brokers support native message headers.
- **Implementation:** Middleware on your producers extracts `x-preview-env` and attaches it as a Kafka Record Header. Consumers intercept and inject it back into local thread context.
- **Pros:** Zero payload modification required.
- **Cons:** Legacy systems or older broker clients might strip headers.

### Pattern 4: Query-Param + Cookie Middleware (The v1 Solution)
When external systems (like third-party webhooks) call back into your system, they usually won't set custom headers.
- **Implementation:** Append a **signed, scoped, expiring token** as a query parameter to callback URLs (e.g., `?preview_token=<signed-jwt>`). An API gateway or BFF validates the token signature and expiry, extracts the preview environment identifier, converts it to the standard `x-preview-env` header, and **strips the query parameter** before forwarding to upstream services.
- **Security considerations:**
  - The token MUST be signed (e.g., HMAC-SHA256 or asymmetric JWT) to prevent environment hijack
  - The token SHOULD include an expiry (e.g., 1 hour) to limit replay window
  - The token SHOULD be scoped to the specific callback URL path
  - The raw token value MUST NOT be logged in access logs
  - Strip `?preview_token=` from the forwarded request to avoid leaking into application code

## Decision Matrix

| Scenario | Recommended Pattern |
|----------|---------------------|
| Internal Pub/Sub (Kafka/RabbitMQ) | Pattern 3: Broker Header Forwarding |
| Event Mesh (Knative) | Pattern 1: CloudEvents Extension |
| Long-running Orchestration | Pattern 2: Temporal ContextPropagator |
| 3rd-Party Webhooks (Stripe, Twilio)| Pattern 4: Signed Query-Param Token |

## Reference
*For further reading on how large-scale engineering teams solve async preview environments, see Shopify's approach to routing preview contexts through Resque and Kafka.*
