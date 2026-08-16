package sdk

import (
	"context"
	"os"
)

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

// GetHeaderKey returns the header key used for environment propagation.
// It defaults to "x-preview-env" if DIVERGE_HEADER_KEY is not set.
func GetHeaderKey() string {
	if val := os.Getenv("DIVERGE_HEADER_KEY"); val != "" {
		return val
	}
	return "x-preview-env"
}
