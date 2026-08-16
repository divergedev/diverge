package temporal

import (
	"context"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"

	"github.com/divergedev/diverge/pkg/sdk"
)

// InterceptorOption configures the Temporal interceptors.
type InterceptorOption func(*interceptorOptions)

type interceptorOptions struct {
	headerKey string
}

// WithHeaderKey configures a custom header key to use for preview environments.
func WithHeaderKey(key string) InterceptorOption {
	return func(o *interceptorOptions) {
		o.headerKey = key
	}
}

// HeadersProvider implements Temporal's client.HeadersProvider
type HeadersProvider struct {
	// EnvName, when set, overrides context-derived env name.
	// SECURITY: prevents preview sandbox escape.
	EnvName string
	// HeaderKey overrides the default header key if set.
	HeaderKey string
}

func (p HeadersProvider) getHeaderKey() string {
	if p.HeaderKey != "" {
		return p.HeaderKey
	}
	return sdk.GetHeaderKey()
}

func (p HeadersProvider) GetHeaders(ctx context.Context) (map[string]*commonpb.Payload, error) {
	env := p.EnvName
	if env == "" {
		env = sdk.EnvironmentFromContext(ctx)
	}
	if env == "" {
		return nil, nil
	}
	payload, err := converter.GetDefaultDataConverter().ToPayload(env)
	if err != nil {
		return nil, err
	}
	return map[string]*commonpb.Payload{
		p.getHeaderKey(): payload,
	}, nil
}

// WorkerInterceptor intercepts Temporal workflow and activity tasks to extract the preview env header.
type WorkerInterceptor struct {
	interceptor.WorkerInterceptorBase
	headerKey string
}

func NewWorkerInterceptor(opts ...InterceptorOption) interceptor.WorkerInterceptor {
	options := &interceptorOptions{headerKey: sdk.GetHeaderKey()}
	for _, opt := range opts {
		opt(options)
	}
	return &WorkerInterceptor{headerKey: options.headerKey}
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
		if payload, ok := headers[a.root.headerKey]; ok {
			var env string
			if err := converter.GetDefaultDataConverter().FromPayload(payload, &env); err == nil && env != "" {
				ctx = context.WithValue(ctx, sdk.EnvContextKey, env)
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
		if payload, ok := headers[w.root.headerKey]; ok {
			var env string
			if err := converter.GetDefaultDataConverter().FromPayload(payload, &env); err == nil && env != "" {
				ctx = workflow.WithValue(ctx, sdk.EnvContextKey, env)
			}
		}
	}
	return w.Next.ExecuteWorkflow(ctx, in)
}
