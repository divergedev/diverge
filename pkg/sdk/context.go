package sdk

import (
	"context"
	"os"
)

type envContextKey struct{}

// EnvContextKey is the key used to store the environment name in a context.
var EnvContextKey = envContextKey{}

// DefaultHeaderKey is used to extract the preview environment name from the HTTP header.
const DefaultHeaderKey = "x-preview-env"

// GetHeaderKey returns the header key from DIVERGE_HEADER_KEY or DefaultHeaderKey.
func GetHeaderKey() string {
	if k := os.Getenv("DIVERGE_HEADER_KEY"); k != "" {
		return k
	}
	return DefaultHeaderKey
}

// WithEnvironment returns a context with the environment name set.
func WithEnvironment(ctx context.Context, envName string) context.Context {
	return context.WithValue(ctx, EnvContextKey, envName)
}

// EnvironmentFromContext extracts the environment name from the context.
func EnvironmentFromContext(ctx context.Context) string {
	if val, ok := ctx.Value(EnvContextKey).(string); ok {
		return val
	}
	return os.Getenv("DIVERGE_ENV")
}
