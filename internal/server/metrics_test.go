package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
)

type mockAnyRequest struct {
	connect.AnyRequest
	spec connect.Spec
}

func (m *mockAnyRequest) Spec() connect.Spec {
	return m.spec
}

func TestMetricsInterceptor_Unary(t *testing.T) {
	interceptor := NewMetricsInterceptor()

	// Reset metrics
	rpcRequestsTotal.Reset()
	rpcRequestDuration.Reset()

	unaryFunc := interceptor.WrapUnary(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, nil
	})

	req := &mockAnyRequest{
		spec: connect.Spec{Procedure: "/diverge.v1alpha1.TestService/SuccessMethod"},
	}
	_, err := unaryFunc(context.Background(), req)
	assert.NoError(t, err)

	unaryFuncErr := interceptor.WrapUnary(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad stuff"))
	})

	reqErr := &mockAnyRequest{
		spec: connect.Spec{Procedure: "/diverge.v1alpha1.TestService/ErrorMethod"},
	}
	_, err = unaryFuncErr(context.Background(), reqErr)
	assert.Error(t, err)

	// Fetch metrics
	metrics, _ := prometheus.DefaultGatherer.Gather()
	foundSuccess := false
	foundError := false

	for _, mf := range metrics {
		if *mf.Name == "diverge_server_rpc_requests_total" {
			for _, metric := range mf.Metric {
				var method, code string
				for _, label := range metric.Label {
					if *label.Name == "method" {
						method = *label.Value
					}
					if *label.Name == "code" {
						code = *label.Value
					}
				}
				if method == "/diverge.v1alpha1.TestService/SuccessMethod" && code == "ok" {
					foundSuccess = true
					assert.Equal(t, float64(1), *metric.Counter.Value)
				}
				if method == "/diverge.v1alpha1.TestService/ErrorMethod" && code == "invalid_argument" {
					foundError = true
					assert.Equal(t, float64(1), *metric.Counter.Value)
				}
			}
		}
	}
	assert.True(t, foundSuccess)
	assert.True(t, foundError)
}

type mockStreamingHandlerConn struct {
	connect.StreamingHandlerConn
	spec connect.Spec
}

func (m *mockStreamingHandlerConn) Spec() connect.Spec {
	return m.spec
}

func TestMetricsInterceptor_Streaming(t *testing.T) {
	interceptor := NewMetricsInterceptor()

	rpcRequestsTotal.Reset()
	rpcRequestDuration.Reset()

	streamWait := make(chan struct{})
	streamStart := make(chan struct{})

	streamFunc := interceptor.WrapStreamingHandler(func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		close(streamStart)
		<-streamWait
		return nil
	})

	conn := &mockStreamingHandlerConn{
		spec: connect.Spec{Procedure: "/diverge.v1alpha1.TestService/StreamMethod"},
	}

	go func() {
		_ = streamFunc(context.Background(), conn)
	}()

	<-streamStart

	// Check active streams
	metrics, _ := prometheus.DefaultGatherer.Gather()
	activeCount := 0.0
	for _, mf := range metrics {
		if *mf.Name == "diverge_server_rpc_active_streams" {
			activeCount = *mf.Metric[0].Gauge.Value
		}
	}
	assert.Equal(t, float64(1), activeCount)

	close(streamWait)

	// Wait for stream to finish
	time.Sleep(50 * time.Millisecond)

	metrics2, _ := prometheus.DefaultGatherer.Gather()
	activeCount2 := 0.0
	for _, mf := range metrics2 {
		if *mf.Name == "diverge_server_rpc_active_streams" {
			activeCount2 = *mf.Metric[0].Gauge.Value
		}
	}
	assert.Equal(t, float64(0), activeCount2)
}

func TestMetricsEndpoint(t *testing.T) {
	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, strings.Contains(rr.Body.String(), "diverge_server_rpc_active_streams"))
}
