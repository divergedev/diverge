package temporal

import (
	"context"
	"os"

	"go.temporal.io/sdk/workflow"

	"github.com/divergedev/diverge/pkg/sdk"
)

// WithEnv returns a context with the preview environment name set.
func WithEnv(ctx context.Context, env string) context.Context {
	return context.WithValue(ctx, sdk.EnvContextKey, env)
}

// EnvFromContext extracts the preview environment name from a context.
// Falls back to DIVERGE_ENV env var if not in context.
func EnvFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(sdk.EnvContextKey).(string); ok {
		return v
	}
	return os.Getenv("DIVERGE_ENV")
}

// EnvFromWorkflowContext extracts the preview environment name from a workflow context.
// Falls back to DIVERGE_ENV env var if not in context.
func EnvFromWorkflowContext(ctx workflow.Context) string {
	if v, ok := ctx.Value(sdk.EnvContextKey).(string); ok {
		return v
	}
	return os.Getenv("DIVERGE_ENV")
}
