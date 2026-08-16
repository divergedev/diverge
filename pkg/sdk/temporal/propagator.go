package temporal

import (
	"context"

	"github.com/divergedev/diverge/pkg/sdk"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

// PropagatorOption configures the Propagator.
type PropagatorOption func(*Propagator)

// WithPropagatorHeaderKey sets the header key for the propagator.
func WithPropagatorHeaderKey(key string) PropagatorOption {
	return func(p *Propagator) {
		p.headerKey = key
	}
}

// Propagator implements workflow.ContextPropagator.
// It propagates the preview environment context through Temporal workflow headers.
//
// SECURITY: In preview environments, this propagator OVERWRITES the env header
// with the configured environment name. User-provided context is untrusted.
type Propagator struct {
	// EnvName is the preview environment name.
	// When set, it overrides any user-provided env context (sandbox escape prevention).
	EnvName string
	headerKey string
}

func NewPropagator(opts ...PropagatorOption) *Propagator {
	p := &Propagator{
		headerKey: sdk.GetHeaderKey(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

var _ workflow.ContextPropagator = (*Propagator)(nil)

// Inject injects the environment name into workflow headers.
func (p *Propagator) Inject(ctx context.Context, writer workflow.HeaderWriter) error {
	env := p.EnvName
	if env == "" {
		// Not in a preview environment, try to read from context
		env = sdk.EnvironmentFromContext(ctx)
	}
	if env != "" {
		payload, err := converter.GetDefaultDataConverter().ToPayload(env)
		if err != nil {
			return err
		}
		writer.Set(p.headerKey, payload)
	}
	return nil
}

// Extract extracts the environment name from workflow headers.
func (p *Propagator) Extract(ctx context.Context, reader workflow.HeaderReader) (context.Context, error) {
	// If EnvName is set (preview mode), ALWAYS use it regardless of header
	if p.EnvName != "" {
		return context.WithValue(ctx, sdk.EnvContextKey, p.EnvName), nil
	}
	// Otherwise read from header
	payload, ok := reader.Get(p.headerKey)
	if !ok {
		return ctx, nil // no header, not in preview
	}
	var env string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &env); err != nil {
		return ctx, nil
	}
	return context.WithValue(ctx, sdk.EnvContextKey, env), nil
}

// InjectFromWorkflow injects the environment name from the workflow context.
func (p *Propagator) InjectFromWorkflow(ctx workflow.Context, writer workflow.HeaderWriter) error {
	// Same as Inject but for workflow context
	env := p.EnvName
	if env == "" {
		// Try to get from workflow context
		if v, ok := ctx.Value(sdk.EnvContextKey).(string); ok {
			env = v
		}
	}
	if env != "" {
		payload, err := converter.GetDefaultDataConverter().ToPayload(env)
		if err != nil {
			return err
		}
		writer.Set(p.headerKey, payload)
	}
	return nil
}

// ExtractToWorkflow extracts the environment name into the workflow context.
func (p *Propagator) ExtractToWorkflow(ctx workflow.Context, reader workflow.HeaderReader) (workflow.Context, error) {
	if p.EnvName != "" {
		return workflow.WithValue(ctx, sdk.EnvContextKey, p.EnvName), nil
	}
	payload, ok := reader.Get(p.headerKey)
	if !ok {
		return ctx, nil
	}
	var env string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &env); err != nil {
		return ctx, nil
	}
	return workflow.WithValue(ctx, sdk.EnvContextKey, env), nil
}

// EnvFromWorkflow extracts the preview environment name from workflow context.
func EnvFromWorkflow(ctx workflow.Context) string {
	if v, ok := ctx.Value(sdk.EnvContextKey).(string); ok {
		return v
	}
	return ""
}
