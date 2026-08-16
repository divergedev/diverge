package temporal

import (
	"context"
	"os"

	"go.temporal.io/sdk/workflow"
)

type contextKey struct{}
var envContextKey = contextKey{}

func WithEnv(ctx context.Context, env string) context.Context {
	return context.WithValue(ctx, envContextKey, env)
}

func EnvFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(envContextKey).(string); ok {
		return v
	}
	return os.Getenv("DIVERGE_ENV")
}

func EnvFromWorkflowContext(ctx workflow.Context) string {
	if v, ok := ctx.Value(envContextKey).(string); ok {
		return v
	}
	return os.Getenv("DIVERGE_ENV")
}
