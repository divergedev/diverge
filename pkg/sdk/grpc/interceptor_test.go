package grpc

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/pkg/sdk"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestUnaryServerInterceptor(t *testing.T) {
	interceptor := UnaryServerInterceptor()
	called := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		called = true
		env := sdk.EnvironmentFromContext(ctx)
		assert.Equal(t, "pr-123", env)
		return nil, nil
	}

	md := metadata.Pairs(sdk.DefaultHeaderKey, "pr-123")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err := interceptor(ctx, nil, nil, handler)
	assert.NoError(t, err)
	assert.True(t, called, "handler must be invoked")
}

func TestUnaryClientInterceptor(t *testing.T) {
	interceptor := UnaryClientInterceptor()
	called := false

	ctx := sdk.WithEnvironment(context.Background(), "pr-123")

	invoker := func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		called = true
		md, ok := metadata.FromOutgoingContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, []string{"pr-123"}, md.Get(sdk.DefaultHeaderKey))
		return nil
	}

	err := interceptor(ctx, "/method", nil, nil, nil, invoker)
	assert.NoError(t, err)
	assert.True(t, called, "invoker must be invoked")
}

func TestUnaryClientInterceptorReplacesExisting(t *testing.T) {
	interceptor := UnaryClientInterceptor()
	called := false

	ctx := sdk.WithEnvironment(context.Background(), "pr-123")
	md := metadata.Pairs(sdk.DefaultHeaderKey, "old-value", "other-key", "other-value")
	ctx = metadata.NewOutgoingContext(ctx, md)

	invoker := func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		called = true
		md, ok := metadata.FromOutgoingContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, []string{"pr-123"}, md.Get(sdk.DefaultHeaderKey))
		assert.Equal(t, []string{"other-value"}, md.Get("other-key"))
		return nil
	}

	err := interceptor(ctx, "/method", nil, nil, nil, invoker)
	assert.NoError(t, err)
	assert.True(t, called, "invoker must be invoked")
}

type mockServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}

func TestStreamServerInterceptor(t *testing.T) {
	interceptor := StreamServerInterceptor()
	called := false
	handler := func(srv interface{}, stream grpc.ServerStream) error {
		called = true
		env := sdk.EnvironmentFromContext(stream.Context())
		assert.Equal(t, "pr-123", env)
		return nil
	}

	md := metadata.Pairs(sdk.DefaultHeaderKey, "pr-123")
	ctx := metadata.NewIncomingContext(context.Background(), md)
	ss := &mockServerStream{ctx: ctx}

	err := interceptor(nil, ss, nil, handler)
	assert.NoError(t, err)
	assert.True(t, called, "handler must be invoked")
}

func TestStreamClientInterceptor(t *testing.T) {
	interceptor := StreamClientInterceptor()
	called := false

	ctx := sdk.WithEnvironment(context.Background(), "pr-123")

	streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		called = true
		md, ok := metadata.FromOutgoingContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, []string{"pr-123"}, md.Get(sdk.DefaultHeaderKey))
		return nil, nil
	}

	_, err := interceptor(ctx, nil, nil, "/method", streamer)
	assert.NoError(t, err)
	assert.True(t, called, "streamer must be invoked")
}
