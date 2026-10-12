package server

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
)

type fakeStreamingHandlerConn struct {
	connect.StreamingHandlerConn
	header http.Header
}

func (f *fakeStreamingHandlerConn) RequestHeader() http.Header {
	return f.header
}

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"Bearer valid-token-123", "valid-token-123"},
		{"bearer lower-case", "lower-case"},
		{"BEARER upper-case", "upper-case"},
		{"Basic dXNlcjpwYXNz", ""},
		{"Bearer", ""},
		{"", ""},
		{"Bearer tok1 tok2", ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, extractBearerToken(tt.header), "header=%q", tt.header)
	}
}

func TestAuthInterceptor_UnaryAndStreaming(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "test_auth_attempts_total",
		Help: "Test counter for auth interceptor",
	}, []string{"provider", "result"})

	interceptor := NewAuthInterceptor(counter)

	unaryNext := interceptor.WrapUnary(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&pb.GetClusterInfoResponse{ControllerVersion: "v1.0.0"}), nil
	})

	// 1. Missing Authorization header -> CodeUnauthenticated
	reqMissing := connect.NewRequest(&pb.GetClusterInfoRequest{})
	_, err := unaryNext(context.Background(), reqMissing)
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	// 2. Invalid token -> CodeUnauthenticated
	reqInvalid := connect.NewRequest(&pb.GetClusterInfoRequest{})
	reqInvalid.Header().Set("Authorization", "Bearer invalid-token")
	_, err = unaryNext(context.Background(), reqInvalid)
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	// 3. Valid token -> succeeds
	reqValid := connect.NewRequest(&pb.GetClusterInfoRequest{})
	reqValid.Header().Set("Authorization", "Bearer valid-token")
	resp, err := unaryNext(context.Background(), reqValid)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// 4. Streaming handler checks
	streamCalled := false
	streamNext := interceptor.WrapStreamingHandler(func(_ context.Context, _ connect.StreamingHandlerConn) error {
		streamCalled = true
		return nil
	})

	err = streamNext(context.Background(), &fakeStreamingHandlerConn{header: http.Header{}})
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	badHdr := http.Header{}
	badHdr.Set("Authorization", "Bearer invalid-token")
	err = streamNext(context.Background(), &fakeStreamingHandlerConn{header: badHdr})
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	goodHdr := http.Header{}
	goodHdr.Set("Authorization", "Bearer good-stream-token")
	err = streamNext(context.Background(), &fakeStreamingHandlerConn{header: goodHdr})
	require.NoError(t, err)
	assert.True(t, streamCalled)

	// 5. WrapStreamingClient is a pass-through
	clientFn := interceptor.WrapStreamingClient(func(_ context.Context, _ connect.Spec) connect.StreamingClientConn {
		return nil
	})
	assert.NotNil(t, clientFn)

	assert.Equal(t, float64(4), testutil.ToFloat64(counter.WithLabelValues("bearer", "failure")))
	assert.Equal(t, float64(2), testutil.ToFloat64(counter.WithLabelValues("bearer", "success")))
}
