package http

import (
	"context"
	"net/http"

	"github.com/divergedev/diverge/pkg/sdk"
)

// DefaultHeaderKey is used by the middleware to extract the X-Diverge-Env header and inject it into the request context.
const DefaultHeaderKey = sdk.DefaultHeaderKey

// PropagateEnvironment returns middleware that propagates the x-diverge-env
// header from incoming requests to outgoing requests via the request context.
//
// Example:
//
//	// Incoming handler:
//	mux.Use(divergehttp.PropagateEnvironment)
//
//	// Outgoing client:
//	client := &http.Client{
//	    Transport: divergehttp.RoundTripper(http.DefaultTransport),
//	}
func PropagateEnvironment(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := r.Header.Get(DefaultHeaderKey)
		if env != "" {
			ctx := sdk.WithEnvironment(r.Context(), env)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// RoundTripper wraps an http.RoundTripper to inject x-diverge-env from context
// into outgoing HTTP requests.
func RoundTripper(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &roundTripper{base: base}
}

type roundTripper struct {
	base http.RoundTripper
}

// RoundTrip injects the environment context into outgoing requests.
func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	env := sdk.EnvironmentFromContext(req.Context())
	if env != "" {
		// Clone request to avoid modifying original
		req = req.Clone(req.Context())
		req.Header.Set(DefaultHeaderKey, env)
	}
	return rt.base.RoundTrip(req)
}

// FromContext extracts the environment name from the context.
func FromContext(ctx context.Context) string {
	return sdk.EnvironmentFromContext(ctx)
}
