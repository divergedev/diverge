# Diverge SDK

Provides context propagation and resource isolation helpers for Diverge's async routing (Temporal, Kafka, and Redis/Valkey).
When a service in a preview environment calls Temporal, publishes to Kafka, or accesses shared Redis/Valkey caches, the routing header (`x-diverge-env`) and key prefixes propagate so preview traffic stays isolated from production.

## Usage

### Temporal Worker Setup

```go
import "github.com/divergedev/diverge/pkg/sdk/temporal"

w := worker.New(c, temporal.TaskQueue("my-queue"), worker.Options{
    ContextPropagators: []workflow.ContextPropagator{
        temporal.NewContextPropagator(),
    },
})
```

### Kafka Producer

```go
import "github.com/divergedev/diverge/pkg/sdk/kafka"

topic, _ := kafka.Topic("my-topic", os.Getenv("DIVERGE_ENV"))
headers := kafka.Headers() // returns map[string]string with x-diverge-env
```

### Redis / Valkey Key Prefix Isolation

```go
import "github.com/divergedev/diverge/pkg/sdk"

key := sdk.ResolveRedisKey(ctx, "session:user-123")
// Returns "preview:pr-42:session:user-123" in preview environments, or "session:user-123" in baseline
```
