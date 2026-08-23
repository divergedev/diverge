package temporal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
)

func TestConfigure(t *testing.T) {
	var clientOpts client.Options
	var interceptors []interceptor.WorkerInterceptor

	Configure(&clientOpts, &interceptors)

	// Should add a context propagator
	assert.Len(t, clientOpts.ContextPropagators, 1)
	// Should set a headers provider
	require.NotNil(t, clientOpts.HeadersProvider)
	// HeadersProvider should have empty EnvName in production
	hp, ok := clientOpts.HeadersProvider.(HeadersProvider)
	require.True(t, ok)
	assert.Empty(t, hp.EnvName)
	// Should add a worker interceptor
	assert.Len(t, interceptors, 1)
}

func TestConfigure_WithEnv(t *testing.T) {
	t.Setenv("DIVERGE_ENV", "pr-42")

	var clientOpts client.Options
	var interceptors []interceptor.WorkerInterceptor

	Configure(&clientOpts, &interceptors)

	// Propagator should have EnvName set
	propagator, ok := clientOpts.ContextPropagators[0].(*Propagator)
	require.True(t, ok)
	assert.Equal(t, "pr-42", propagator.EnvName)

	// HeadersProvider should have EnvName set
	hp, ok := clientOpts.HeadersProvider.(HeadersProvider)
	require.True(t, ok)
	assert.Equal(t, "pr-42", hp.EnvName)

	// Should add interceptor
	assert.Len(t, interceptors, 1)
}
