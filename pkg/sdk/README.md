# Diverge SDK

Provides context propagation for Diverge's async routing (Temporal and Kafka).
When a service in a preview environment calls Temporal or publishes to Kafka, the routing header (`x-diverge-env`) must propagate so downstream workers/consumers in the preview environment pick up the work instead of production workers.

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
