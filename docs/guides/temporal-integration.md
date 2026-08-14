# Temporal Integration Guide

Diverge provides a ContextPropagator for Temporal to route workflow and activity execution to preview environments.

## How it Works

When you create a preview environment, Diverge automatically creates a ConfigMap in the target namespace containing the environment routing configuration. Your Temporal workers read this config at startup (using the Diverge ContextPropagator) to determine if they are running in a preview environment.

The propagator intercepts Temporal headers and dynamically routes task execution to the correct environment.

## 1. Install the SDK

The Diverge Temporal SDK is published as a standalone module to prevent pulling Temporal dependencies into your main application if you aren't using them.

```bash
go get github.com/divergedev/diverge/pkg/sdk/temporal
```

## 2. Register the ContextPropagator

When creating your Temporal Client, register the Diverge ContextPropagator:

```go
import (
	divergetemporal "github.com/divergedev/diverge/pkg/sdk/temporal"
	"go.temporal.io/sdk/client"
)

func main() {
	// The propagator will automatically read from the injected environment
	// if running inside a Diverge preview environment.
	env := os.Getenv("DIVERGE_ENV") // Or read from the diverging ConfigMap

	propagator := &divergetemporal.Propagator{
		EnvName: env,
	}

	c, err := client.Dial(client.Options{
		HostPort:          "localhost:7233",
		ContextPropagators: []workflow.ContextPropagator{propagator},
	})
	if err != nil {
		log.Fatalln("Unable to create client", err)
	}
	defer c.Close()

	// ... start worker or workflows
}
```

## 3. Task Queue Isolation

Because Temporal workflows use task queues rather than HTTP routes, preview environment workers must listen on a specific task queue to avoid picking up production tasks (and vice versa).

When deploying your worker in a preview environment, append the environment name to the task queue name:

```go
// Base task queue
taskQueue := "my-app-queue"

// If in preview environment, append the env name
if env != "" {
	taskQueue = fmt.Sprintf("%s-%s", taskQueue, env)
}

w := worker.New(c, taskQueue, worker.Options{})
```

The Diverge ConfigMap creates a `task-queue-suffix` key containing the environment name for this exact purpose.

## Security

In preview mode (when `EnvName` is set on the Propagator), the propagator **overwrites** the `x-diverge-env` header injected into the workflow context. This guarantees that untrusted user input cannot forge headers and break out of the preview sandbox environment.

## Limitations

### Scale-to-Zero

In Phase 3, asynchronous workers (like Temporal workers) cannot be scaled to zero using the KEDA HTTP Add-on because the add-on only intercepts HTTP traffic. Since Temporal workers connect outwards to the Temporal cluster via gRPC to poll for tasks, there is no incoming HTTP request to trigger a scale-up.

For now, Temporal workers in preview environments must remain running (at least 1 replica).
