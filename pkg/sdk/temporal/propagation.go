package temporal

import (
	"context"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
)

const HeaderKey = "x-diverge-env"

type ContextPropagator struct{}

func NewContextPropagator() *ContextPropagator {
	return &ContextPropagator{}
}

func (c *ContextPropagator) Inject(ctx context.Context, writer workflow.HeaderWriter) error {
	if env := EnvFromContext(ctx); env != "" {
		writer.Set(HeaderKey, []byte(env))
	}
	return nil
}

func (c *ContextPropagator) Extract(ctx context.Context, reader workflow.HeaderReader) (context.Context, error) {
	if val, ok := reader.Get(HeaderKey); ok {
		ctx = WithEnv(ctx, string(val))
	}
	return ctx, nil
}

func (c *ContextPropagator) InjectFromWorkflow(ctx workflow.Context, writer workflow.HeaderWriter) error {
	if env := EnvFromWorkflowContext(ctx); env != "" {
		writer.Set(HeaderKey, []byte(env))
	}
	return nil
}

func (c *ContextPropagator) ExtractToWorkflow(ctx workflow.Context, reader workflow.HeaderReader) (workflow.Context, error) {
	if val, ok := reader.Get(HeaderKey); ok {
		ctx = workflow.WithValue(ctx, envContextKey, string(val))
	}
	return ctx, nil
}
