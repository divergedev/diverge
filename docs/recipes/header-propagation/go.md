# Go Header Propagation Recipe

This recipe demonstrates how to propagate the `x-preview-env` header across both HTTP (net/http) and gRPC services in Go.

## 1. HTTP Middleware (Inbound)

First, we need middleware to extract the header from incoming requests and inject it into the request context.

```go
package middleware

import (
	"context"
	"net/http"
)

const PreviewEnvHeader = "x-preview-env"

type contextKey string
const PreviewEnvContextKey = contextKey(PreviewEnvHeader)

// PreviewEnvMiddleware extracts the x-preview-env header and adds it to the context
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

## OpenTelemetry Baggage (Alternative)

If you are already using OpenTelemetry, you can propagate the preview environment as Baggage instead of manual header propagation. The standard OpenTelemetry propagators will automatically forward Baggage items across service boundaries.
