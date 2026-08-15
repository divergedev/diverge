package grpc

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/divergedev/diverge/pkg/sdk"
)

// UnaryClientInterceptor propagates x-diverge-env metadata from the incoming
// context to outgoing gRPC calls.
//
// Example Client:
//
//	grpc.Dial(addr,
//	    grpc.WithUnaryInterceptor(divergegrpc.UnaryClientInterceptor()),
//	    grpc.WithStreamInterceptor(divergegrpc.StreamClientInterceptor()),
//	)
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		env := sdk.EnvironmentFromContext(ctx)
		if env != "" {
			md, ok := metadata.FromOutgoingContext(ctx)
			if !ok {
				md = metadata.New(nil)
			} else {
				md = md.Copy()
			}
			md.Set(sdk.DefaultHeaderKey, env)
			ctx = metadata.NewOutgoingContext(ctx, md)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// StreamClientInterceptor propagates x-diverge-env metadata from the incoming
// context to outgoing gRPC streams.
func StreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		env := sdk.EnvironmentFromContext(ctx)
		if env != "" {
			md, ok := metadata.FromOutgoingContext(ctx)
			if !ok {
				md = metadata.New(nil)
			} else {
				md = md.Copy()
			}
			md.Set(sdk.DefaultHeaderKey, env)
			ctx = metadata.NewOutgoingContext(ctx, md)
		}
		return streamer(ctx, desc, cc, method, opts...)
	}
}

// UnaryServerInterceptor extracts x-diverge-env from incoming gRPC metadata
// and stores it in the context for downstream propagation.
//
// Example Server:
//
//	grpc.NewServer(
//	    grpc.UnaryInterceptor(divergegrpc.UnaryServerInterceptor()),
//	    grpc.StreamInterceptor(divergegrpc.StreamServerInterceptor()),
//	)
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get(sdk.DefaultHeaderKey); len(vals) > 0 {
				ctx = sdk.WithEnvironment(ctx, vals[0])
			}
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor extracts x-diverge-env from incoming gRPC metadata
// and stores it in the context.
func StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get(sdk.DefaultHeaderKey); len(vals) > 0 {
				ctx = sdk.WithEnvironment(ctx, vals[0])
			}
		}
		wrapped := &wrappedStream{ServerStream: ss, ctx: ctx}
		return handler(srv, wrapped)
	}
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

// Context performs its designated operation.
func (w *wrappedStream) Context() context.Context {
	return w.ctx
}
