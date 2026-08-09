package connect

import (
	"context"

	"connectrpc.com/connect"
	"github.com/divergedev/diverge/pkg/sdk"
)

// PropagateEnvironment returns a ConnectRPC interceptor that propagates
// the x-diverge-env header between incoming and outgoing requests.
//
// Example:
//
//	client := examplev1connect.NewExampleServiceClient(
//	    http.DefaultClient,
//	    "http://localhost:8080",
//	    connect.WithInterceptors(divergeconnect.PropagateEnvironment()),
//	)
func PropagateEnvironment() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// Extract from incoming context (if on server, or if set manually on client)
			env := sdk.EnvironmentFromContext(ctx)

			// Also try to extract from headers (if acting as server)
			if env == "" && req.Header().Get(sdk.DefaultHeaderKey) != "" {
				env = req.Header().Get(sdk.DefaultHeaderKey)
				ctx = sdk.WithEnvironment(ctx, env)
			}

			// Inject to outgoing headers (if acting as client)
			if env != "" {
				req.Header().Set(sdk.DefaultHeaderKey, env)
			}

			return next(ctx, req)
		}
	})
}
