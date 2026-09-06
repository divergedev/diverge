package openfeature

import (
	"context"

	of "github.com/open-feature/go-sdk/openfeature"

	"github.com/divergedev/diverge/pkg/sdk"
)

// Option is a functional option for configuring DivergeHook.
type Option func(*DivergeHook)

// WithFallbackEnvironment sets a default environment if none is found in the context or process env.
func WithFallbackEnvironment(fallback string) Option {
	return func(h *DivergeHook) {
		h.fallbackEnv = fallback
	}
}

// DivergeHook is an OpenFeature hook that enriches the EvaluationContext with Diverge preview environment metadata.
type DivergeHook struct {
	of.UnimplementedHook
	fallbackEnv string
}

// NewDivergeHook creates a new DivergeHook with optional configuration.
func NewDivergeHook(opts ...Option) *DivergeHook {
	h := &DivergeHook{}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Before executes prior to flag evaluation, injecting diverge.environment and diverge.is_preview into EvaluationContext.
func (h *DivergeHook) Before(ctx context.Context, hookCtx of.HookContext, hints of.HookHints) (*of.EvaluationContext, error) {
	envName := sdk.EnvironmentFromContext(ctx)
	if envName == "" {
		envName = h.fallbackEnv
	}

	existingAttrs := hookCtx.EvaluationContext().Attributes()
	attrs := make(map[string]any, len(existingAttrs)+3)

	for k, v := range existingAttrs {
		attrs[k] = v
	}

	isPreview := envName != ""
	attrs["diverge.environment"] = envName
	attrs["diverge.is_preview"] = isPreview

	targetingKey := hookCtx.EvaluationContext().TargetingKey()
	evalCtx := of.NewEvaluationContext(targetingKey, attrs)

	return &evalCtx, nil
}
