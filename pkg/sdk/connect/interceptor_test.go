package connect

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/divergedev/diverge/pkg/sdk"
	"github.com/stretchr/testify/assert"
)

func TestInterceptorExtractsHeader(t *testing.T) {
	interceptor := PropagateEnvironment()
	next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		env := sdk.EnvironmentFromContext(ctx)
		assert.Equal(t, "pr-123", env)
		return nil, nil
	})

	req := connect.NewRequest(&struct{}{})
	req.Header().Set(sdk.DefaultHeaderKey, "pr-123")

	unary := interceptor.WrapUnary(next)
	_, err := unary(context.Background(), req)
	assert.NoError(t, err)
}

func TestInterceptorInjectsHeader(t *testing.T) {
	interceptor := PropagateEnvironment()
	next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		assert.Equal(t, "pr-123", req.Header().Get(sdk.DefaultHeaderKey))
		return nil, nil
	})

	ctx := sdk.WithEnvironment(context.Background(), "pr-123")
	req := connect.NewRequest(&struct{}{})
	// Reset to make sure header isn't already there
	req.Header().Del(sdk.DefaultHeaderKey)

	unary := interceptor.WrapUnary(next)
	_, err := unary(ctx, req)
	assert.NoError(t, err)
}
