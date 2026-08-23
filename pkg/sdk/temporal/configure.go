package temporal

import (
	"os"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
)

// Configure sets up Temporal client and worker options for diverge preview environments.
// It configures the context propagator, headers provider, and worker interceptor
// in a single call, reducing setup from ~15 lines to 1.
//
// Usage:
//
//	var clientOpts client.Options
//	var workerOpts worker.Options
//	divergetemporal.Configure(&clientOpts, &workerOpts.Interceptors)
//	c, err := client.Dial(clientOpts)
//	w := worker.New(c, divergetemporal.TaskQueue("orders"), workerOpts)
func Configure(clientOpts *client.Options, workerInterceptors *[]interceptor.WorkerInterceptor, opts ...InterceptorOption) {
	envName := os.Getenv("DIVERGE_ENV")

	// Set up context propagator
	propagator := NewPropagator()
	if envName != "" {
		propagator.EnvName = envName
	}
	clientOpts.ContextPropagators = append(clientOpts.ContextPropagators, propagator)

	// Set up headers provider
	clientOpts.HeadersProvider = HeadersProvider{EnvName: envName}

	// Set up worker interceptor — envName appended last to prevent caller override
	allOpts := append([]InterceptorOption{}, opts...)
	if envName != "" {
		allOpts = append(allOpts, WithEnvName(envName))
	}
	*workerInterceptors = append(*workerInterceptors, NewWorkerInterceptor(allOpts...))
}
