package server

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/divergedev/diverge/internal/server/streaming"
)

var (
	rpcRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "rpc_requests_total",
		Help:      "Total number of RPC requests by method and status",
	}, []string{"method", "code"})

	rpcRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "rpc_request_duration_seconds",
		Help:      "RPC request duration in seconds",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method"})

	rpcActiveStreams = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "rpc_active_streams",
		Help:      "Number of active streaming RPCs",
	})

	authAttemptsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "auth_attempts_total",
		Help:      "Authentication attempts by provider and result",
	}, []string{"provider", "result"})

	broadcasterSubscribers = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "broadcaster_subscribers",
		Help:      "Current number of active broadcaster subscribers",
	})

	broadcasterEventsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "broadcaster_events_total",
		Help:      "Total events published through broadcaster",
	})

	broadcasterDropsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "broadcaster_drops_total",
		Help:      "Total events dropped due to slow consumers",
	})
)

type metricsInterceptor struct{}

func GetBroadcasterMetrics() streaming.BroadcasterMetrics {
	return streaming.BroadcasterMetrics{
		IncSubscribers: func() { broadcasterSubscribers.Inc() },
		DecSubscribers: func() { broadcasterSubscribers.Dec() },
		IncEvents:      func() { broadcasterEventsTotal.Inc() },
		IncDrops:       func() { broadcasterDropsTotal.Inc() },
	}
}

func NewMetricsInterceptor() connect.Interceptor {
	return &metricsInterceptor{}
}

func (m *metricsInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		start := time.Now()
		resp, err := next(ctx, req)
		duration := time.Since(start).Seconds()
		method := req.Spec().Procedure
		code := "ok"
		if err != nil {
			code = connect.CodeOf(err).String()
		}
		rpcRequestsTotal.WithLabelValues(method, code).Inc()
		rpcRequestDuration.WithLabelValues(method).Observe(duration)
		return resp, err
	}
}

func (m *metricsInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (m *metricsInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		rpcActiveStreams.Inc()
		defer rpcActiveStreams.Dec()
		method := conn.Spec().Procedure
		start := time.Now()
		err := next(ctx, conn)
		duration := time.Since(start).Seconds()
		code := "ok"
		if err != nil {
			code = connect.CodeOf(err).String()
		}
		rpcRequestsTotal.WithLabelValues(method, code).Inc()
		rpcRequestDuration.WithLabelValues(method).Observe(duration)
		return err
	}
}
