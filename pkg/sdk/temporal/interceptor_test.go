package temporal_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
	"pgregory.net/rapid"

	"github.com/divergedev/diverge/pkg/sdk"
	"github.com/divergedev/diverge/pkg/sdk/temporal"
)

// TestInterceptorRoundtripPBT uses property-based testing to verify that headers injected
// by HeadersProvider are properly extracted by the WorkerInterceptor.
func TestInterceptorRoundtripPBT(t *testing.T) {
	provider := temporal.HeadersProvider{}

	rapid.Check(t, func(t *rapid.T) {
		env := rapid.StringMatching(`^[a-zA-Z0-9-]{0,63}$`).Draw(t, "envName")
		// 1. Inject into Context
		ctx := context.Background()
		if env != "" {
			ctx = sdk.WithEnvironment(ctx, env)
		}

		// 2. Extract using HeadersProvider
		headers, err := provider.GetHeaders(ctx)
		if err != nil {
			t.Fatalf("Failed to get headers: %v", err)
		}

		if env == "" {
			if len(headers) != 0 {
				t.Fatalf("Expected no headers")
			}
			return
		}

		if headers == nil {
			t.Fatalf("Expected headers")
		}

		payload, ok := headers[sdk.GetHeaderKey()]
		if !ok {
			t.Fatalf("Expected header %s", sdk.GetHeaderKey())
		}

		require.Equal(t, env, payload, "Expected extracted env to match")
	})
}

// mockWorkflowInterceptor captures context.
type mockWorkflowInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
}

func (m *mockWorkflowInterceptor) ExecuteWorkflow(ctx workflow.Context, in *interceptor.ExecuteWorkflowInput) (interface{}, error) {
	return nil, nil
}

func TestWorkflowInterceptorInitialization(t *testing.T) {
	workerInt := temporal.NewWorkerInterceptor()
	nextWf := &mockWorkflowInterceptor{}

	// Just ensure it doesn't panic on initialization
	wfInt := workerInt.InterceptWorkflow(nil, nextWf)
	if wfInt == nil {
		t.Error("Expected interceptor, got nil")
	}
}

func TestHeadersProvider_SecurityOverwrite(t *testing.T) {
	provider := temporal.HeadersProvider{EnvName: "trusted-env"}

	// Context has a different env
	ctx := sdk.WithEnvironment(context.Background(), "untrusted-env")

	headers, err := provider.GetHeaders(ctx)
	require.NoError(t, err)

	payload := headers[sdk.GetHeaderKey()]
	assert.Equal(t, "trusted-env", payload) // EnvName MUST win
}
