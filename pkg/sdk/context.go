package sdk

import "context"

type contextKey struct{}

// DefaultHeaderKey is used to extract the preview environment name from the X-Diverge-Env HTTP header.
const DefaultHeaderKey = "x-diverge-env"

// WithEnvironment returns a context with the environment name set.
func WithEnvironment(ctx context.Context, envName string) context.Context {
	return context.WithValue(ctx, contextKey{}, envName)
}

// EnvironmentFromContext extracts the environment name from the context.
func EnvironmentFromContext(ctx context.Context) string {
	if val, ok := ctx.Value(contextKey{}).(string); ok {
		return val
	}
	return ""
}
