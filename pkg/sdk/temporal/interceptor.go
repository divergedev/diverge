package temporal

import (
	"context"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"

	"github.com/divergedev/diverge/pkg/sdk"
)

// HeadersProvider implements Temporal's client.HeadersProvider
type HeadersProvider struct{}

func (p HeadersProvider) GetHeaders(ctx context.Context) (map[string]*commonpb.Payload, error) {
	env := sdk.EnvironmentFromContext(ctx)
	if env == "" {
		return nil, nil
	}
	payload, err := converter.GetDefaultDataConverter().ToPayload(env)
	if err != nil {
		return nil, err
	}
	return map[string]*commonpb.Payload{
		sdk.DefaultHeaderKey: payload,
	}, nil
}

// WorkerInterceptor intercepts Temporal workflow and activity tasks to extract the x-diverge-env header.
type WorkerInterceptor struct {
	interceptor.WorkerInterceptorBase
}

func NewWorkerInterceptor() interceptor.WorkerInterceptor {
	return &WorkerInterceptor{}
}

func (w *WorkerInterceptor) InterceptActivity(ctx context.Context, next interceptor.ActivityInboundInterceptor) interceptor.ActivityInboundInterceptor {
	i := &activityInboundInterceptor{root: w}
	i.Next = next
	return i
}

type activityInboundInterceptor struct {
	interceptor.ActivityInboundInterceptorBase
	root *WorkerInterceptor
}

func (a *activityInboundInterceptor) ExecuteActivity(ctx context.Context, in *interceptor.ExecuteActivityInput) (interface{}, error) {
	headers := interceptor.Header(ctx)
	if headers != nil {
		if payload, ok := headers[sdk.DefaultHeaderKey]; ok {
			var env string
			if err := converter.GetDefaultDataConverter().FromPayload(payload, &env); err == nil && env != "" {
				ctx = sdk.WithEnvironment(ctx, env)
			}
		}
	}
	return a.Next.ExecuteActivity(ctx, in)
}

func (w *WorkerInterceptor) InterceptWorkflow(ctx workflow.Context, next interceptor.WorkflowInboundInterceptor) interceptor.WorkflowInboundInterceptor {
	i := &workflowInboundInterceptor{root: w}
	i.Next = next
	return i
}

type workflowInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	root *WorkerInterceptor
}

func (w *workflowInboundInterceptor) ExecuteWorkflow(ctx workflow.Context, in *interceptor.ExecuteWorkflowInput) (interface{}, error) {
	headers := interceptor.WorkflowHeader(ctx)
	if headers != nil {
		if payload, ok := headers[sdk.DefaultHeaderKey]; ok {
			var env string
			if err := converter.GetDefaultDataConverter().FromPayload(payload, &env); err == nil && env != "" {
				ctx = workflow.WithValue(ctx, envContextKey, env)
			}
		}
	}
	return w.Next.ExecuteWorkflow(ctx, in)
}
