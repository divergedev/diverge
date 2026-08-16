# Go Header Propagation Recipe

This recipe demonstrates how to propagate the `x-diverge-env` header across both HTTP (net/http) and gRPC services in Go.

## 1. HTTP Middleware (Inbound)

First, we need middleware to extract the header from incoming requests and inject it into the request context.

```go
package middleware

import (
	"context"
	"net/http"
)

const PreviewEnvHeader = "x-diverge-env"

type contextKey string
const PreviewEnvContextKey = contextKey(PreviewEnvHeader)

// PreviewEnvMiddleware extracts the x-diverge-env header and adds it to the context
func PreviewEnvMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		previewEnv := r.Header.Get(PreviewEnvHeader)
		if previewEnv != "" {
			ctx := context.WithValue(r.Context(), PreviewEnvContextKey, previewEnv)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}
```

## 2. HTTP RoundTripper (Outbound)

To propagate the context value to downstream HTTP services, we wrap the `http.RoundTripper`.

```go
package client

import (
	"net/http"
	"middleware" // Import the package where contextKey is defined
)

// PreviewEnvTransport propagates the preview env header to outbound requests
type PreviewEnvTransport struct {
	Base http.RoundTripper
}

func (t *PreviewEnvTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	if val, ok := req.Context().Value(middleware.PreviewEnvContextKey).(string); ok && val != "" {
		// Clone the request to avoid mutating the caller-owned original.
		// Modifying req.Header directly violates the http.RoundTripper contract.
		clone := req.Clone(req.Context())
		clone.Header.Set(middleware.PreviewEnvHeader, val)
		return base.RoundTrip(clone)
	}

	return base.RoundTrip(req)
}

// Example usage:
// client := &http.Client{
// 	Transport: &PreviewEnvTransport{},
// }
```

## 3. gRPC Interceptors

For gRPC, we use interceptors to read and write metadata.

### Unary Server Interceptor (Inbound)

```go
package grpc_interceptor

import (
	"context"
	"middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func PreviewEnvServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			if vals := md.Get(middleware.PreviewEnvHeader); len(vals) > 0 && vals[0] != "" {
				ctx = context.WithValue(ctx, middleware.PreviewEnvContextKey, vals[0])
			}
		}
		return handler(ctx, req)
	}
}
```

### Unary Client Interceptor (Outbound)

```go
package grpc_interceptor

import (
	"context"
	"middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func PreviewEnvClientInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		if val, ok := ctx.Value(middleware.PreviewEnvContextKey).(string); ok && val != "" {
			// Use Set (not Append) to replace any existing preview header,
			// preventing duplicate values on retry or re-invocation.
			md, _ := metadata.FromOutgoingContext(ctx)
			md = md.Copy()
			md.Set(middleware.PreviewEnvHeader, val)
			ctx = metadata.NewOutgoingContext(ctx, md)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
```

## 4. Async Routing SDK (Temporal & Kafka)

Diverge provides SDKs to automatically propagate headers across asynchronous boundaries.

### Temporal

When working with Temporal, you must install the Diverge Temporal SDK to handle header propagation through Temporal metadata and into Go context inside activities.

```go
import (
	divergetemporal "github.com/divergedev/diverge/pkg/sdk/temporal"
)

// Attach the ContextPropagator and HeadersProvider to your client
c, err := client.Dial(client.Options{
	ContextPropagators: []workflow.ContextPropagator{
		&divergetemporal.Propagator{EnvName: env},
	},
	HeadersProvider: divergetemporal.NewHeadersProvider(env),
})

// Attach the WorkerInterceptor to your worker
workerOpts := worker.Options{
	Interceptors: []worker.Interceptor{
		divergetemporal.NewWorkerInterceptor(env),
	},
}
```
*For detailed Temporal guidance, see the [Temporal Integration Guide](../../guides/temporal-integration.md).*

### Kafka

To propagate headers through Kafka messages, you can use `divergekafka.InjectHeaders` and `divergekafka.ExtractEnvName` when constructing or processing messages.

```go
import (
	divergekafka "github.com/divergedev/diverge/pkg/sdk/kafka"
)

// When producing a message, inject the context header
msgHeaders := []divergekafka.Header{
	{Key: "custom-header", Value: []byte("val")},
}
msgHeaders = divergekafka.InjectHeaders(msgHeaders, env)

// When consuming a message, extract the environment name
envName := divergekafka.ExtractEnvName(incomingHeaders)
```

## OpenTelemetry Baggage (Alternative)

If you are already using OpenTelemetry, you can propagate the preview environment as Baggage instead of manual header propagation. The standard OpenTelemetry propagators will automatically forward Baggage items across service boundaries.
