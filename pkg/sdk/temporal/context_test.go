package temporal_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.temporal.io/sdk/workflow"

	"github.com/divergedev/diverge/pkg/sdk"
	"github.com/divergedev/diverge/pkg/sdk/temporal"
)

type stubWorkflowContext struct {
	workflow.Context
	values map[interface{}]interface{}
}

func (s *stubWorkflowContext) Value(key interface{}) interface{} {
	if s.values == nil {
		return nil
	}
	return s.values[key]
}

func TestWithEnvAndEnvFromContext(t *testing.T) {
	t.Run("extracts environment from context set via WithEnv", func(t *testing.T) {
		t.Setenv("DIVERGE_ENV", "fallback-env")
		ctx := temporal.WithEnv(context.Background(), "pr-42")
		assert.Equal(t, "pr-42", temporal.EnvFromContext(ctx))
	})

	t.Run("falls back to DIVERGE_ENV when not in context", func(t *testing.T) {
		t.Setenv("DIVERGE_ENV", "fallback-env")
		assert.Equal(t, "fallback-env", temporal.EnvFromContext(context.Background()))
	})

	t.Run("returns empty string when neither context nor env var set", func(t *testing.T) {
		t.Setenv("DIVERGE_ENV", "")
		assert.Empty(t, temporal.EnvFromContext(context.Background()))
	})
}

func TestEnvFromWorkflowContext(t *testing.T) {
	t.Run("extracts environment from workflow.Context", func(t *testing.T) {
		t.Setenv("DIVERGE_ENV", "fallback-env")
		wfCtx := &stubWorkflowContext{
			values: map[interface{}]interface{}{
				sdk.EnvContextKey: "wf-pr-99",
			},
		}
		assert.Equal(t, "wf-pr-99", temporal.EnvFromWorkflowContext(wfCtx))
	})

	t.Run("falls back to DIVERGE_ENV when not in workflow.Context", func(t *testing.T) {
		t.Setenv("DIVERGE_ENV", "wf-fallback")
		wfCtx := &stubWorkflowContext{}
		assert.Equal(t, "wf-fallback", temporal.EnvFromWorkflowContext(wfCtx))
	})
}
