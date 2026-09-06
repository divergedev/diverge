package openfeature

import (
	"context"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divergedev/diverge/pkg/sdk"
)

type capturingProvider struct {
	of.NoopProvider
	lastCaptured of.FlattenedContext
}

func (c *capturingProvider) BooleanEvaluation(ctx context.Context, flag string, defaultValue bool, flatCtx of.FlattenedContext) of.BoolResolutionDetail {
	c.lastCaptured = flatCtx
	if flatCtx["diverge.environment"] == "pr-789" {
		return of.BoolResolutionDetail{
			Value: true,
		}
	}
	return of.BoolResolutionDetail{
		Value: defaultValue,
	}
}

func TestDivergeHook_Before_WithPreviewEnv(t *testing.T) {
	hook := NewDivergeHook()

	ctx := sdk.WithEnvironment(context.Background(), "pr-456")
	evalCtx := of.NewEvaluationContext("user-1", map[string]any{"user_group": "beta"})
	hookCtx := of.NewHookContext("new-checkout", of.Boolean, false, of.ClientMetadata{}, of.Metadata{}, evalCtx)

	enriched, err := hook.Before(ctx, hookCtx, of.HookHints{})
	require.NoError(t, err)
	require.NotNil(t, enriched)

	assert.Equal(t, "user-1", enriched.TargetingKey())
	assert.Equal(t, "beta", enriched.Attribute("user_group"))
	assert.Equal(t, "pr-456", enriched.Attribute("diverge.environment"))
	assert.Equal(t, true, enriched.Attribute("diverge.is_preview"))
}

func TestDivergeHook_Before_WithoutPreviewEnv(t *testing.T) {
	hook := NewDivergeHook()

	ctx := context.Background()
	evalCtx := of.NewEvaluationContext("user-2", map[string]any{"role": "admin"})
	hookCtx := of.NewHookContext("v2-layout", of.Boolean, false, of.ClientMetadata{}, of.Metadata{}, evalCtx)

	enriched, err := hook.Before(ctx, hookCtx, of.HookHints{})
	require.NoError(t, err)
	require.NotNil(t, enriched)

	assert.Equal(t, "user-2", enriched.TargetingKey())
	assert.Equal(t, "admin", enriched.Attribute("role"))
	assert.Equal(t, "", enriched.Attribute("diverge.environment"))
	assert.Equal(t, false, enriched.Attribute("diverge.is_preview"))
}

func TestDivergeHook_Before_FallbackEnv(t *testing.T) {
	hook := NewDivergeHook(WithFallbackEnvironment("staging"))

	ctx := context.Background()
	evalCtx := of.NewTargetlessEvaluationContext(nil)
	hookCtx := of.NewHookContext("feature-flag", of.Boolean, false, of.ClientMetadata{}, of.Metadata{}, evalCtx)

	enriched, err := hook.Before(ctx, hookCtx, of.HookHints{})
	require.NoError(t, err)
	require.NotNil(t, enriched)

	assert.Equal(t, "staging", enriched.Attribute("diverge.environment"))
	assert.Equal(t, true, enriched.Attribute("diverge.is_preview"))
}

func TestDivergeHook_OpenFeatureClientIntegration(t *testing.T) {
	hook := NewDivergeHook()
	client := of.NewClient("diverge-test")
	client.AddHooks(hook)

	provider := &capturingProvider{}
	err := of.SetNamedProviderAndWait("diverge-test", provider)
	require.NoError(t, err)

	ctx := sdk.WithEnvironment(context.Background(), "pr-789")

	val, err := client.BooleanValue(ctx, "flag-name", false, of.EvaluationContext{})
	require.NoError(t, err)
	assert.True(t, val)
	assert.Equal(t, "pr-789", provider.lastCaptured["diverge.environment"])
	assert.Equal(t, true, provider.lastCaptured["diverge.is_preview"])
}
