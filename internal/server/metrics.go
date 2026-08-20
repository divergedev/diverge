package server

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/divergedev/diverge/internal/server/auth"
	"github.com/divergedev/diverge/internal/server/streaming"
)

var knownProcedures = map[string]bool{
	"/diverge.v1alpha1.EnvironmentService/ListEnvironments":    true,
	"/diverge.v1alpha1.EnvironmentService/GetEnvironment":      true,
	"/diverge.v1alpha1.EnvironmentService/CreateEnvironment":   true,
	"/diverge.v1alpha1.EnvironmentService/UpdateEnvironment":   true,
	"/diverge.v1alpha1.EnvironmentService/DeleteEnvironment":   true,
	"/diverge.v1alpha1.EnvironmentService/ExtendTTL":           true,
	"/diverge.v1alpha1.EnvironmentService/WatchEnvironments":   true,
	"/diverge.v1alpha1.EnvironmentService/StreamLogs":          true,
	"/diverge.v1alpha1.PreviewGroupService/ListPreviewGroups":  true,
	"/diverge.v1alpha1.PreviewGroupService/GetPreviewGroup":    true,
	"/diverge.v1alpha1.PreviewGroupService/CreatePreviewGroup": true,
	"/diverge.v1alpha1.PreviewGroupService/UpdatePreviewGroup": true,
	"/diverge.v1alpha1.PreviewGroupService/DeletePreviewGroup": true,
	"/diverge.v1alpha1.PreviewGroupService/WatchPreviewGroups": true,
	"/diverge.v1alpha1.ClusterService/GetClusterInfo":          true,
	"/diverge.v1alpha1.AuthService/GetCurrentUser":             true,
	"/diverge.v1alpha1.AuthService/ListPermissions":            true,
}

func sanitizeMethod(procedure string) string {
	if knownProcedures[procedure] {
		return procedure
	}
	return "unknown"
}

var (
	rpcRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "rpc_requests_total",
		Help:      "Total number of RPC requests by method and status",
	}, []string{"method", "code"})

	rpcRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "rpc_request_duration_seconds",
		Help:      "RPC request duration in seconds",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method"})

	rpcStreamDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "rpc_stream_duration_seconds",
		Help:      "Duration of streaming RPCs in seconds",
		Buckets:   []float64{1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600},
	}, []string{"method"})

	rpcActiveStreams = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "rpc_active_streams",
		Help:      "Number of active streaming RPCs",
	})

	authAttemptsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "auth_attempts_total",
		Help:      "Authentication attempts by provider and result",
	}, []string{"provider", "result"})

	authLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "auth_latency_seconds",
		Help:      "Time spent authenticating requests via TokenReview",
		Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
	}, []string{"provider", "result"})

	authCacheHits = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "auth_cache_hits_total",
		Help:      "Total auth cache hits",
	})

	authCacheMisses = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "auth_cache_misses_total",
		Help:      "Total auth cache misses",
	})

	broadcasterSubscribers = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "broadcaster_subscribers",
		Help:      "Current number of active broadcaster subscribers",
	})

	broadcasterEventsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "broadcaster_events_total",
		Help:      "Total events published through broadcaster",
	})

	broadcasterDropsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "broadcaster_drops_total",
		Help:      "Total events dropped due to slow consumers",
	})
)

func init() {
	crmetrics.Registry.MustRegister(
		rpcRequestsTotal,
		rpcRequestDuration,
		rpcStreamDuration,
		rpcActiveStreams,
		authAttemptsTotal,
		authLatency,
		authCacheHits,
		authCacheMisses,
		broadcasterSubscribers,
		broadcasterEventsTotal,
		broadcasterDropsTotal,
	)
}

// NewAuthMetrics returns the auth metrics wired to the auth middleware's expected types.
func NewAuthMetrics() *auth.AuthMetrics {
	return &auth.AuthMetrics{
		Latency:     authLatency,
		CacheHits:   authCacheHits,
		CacheMisses: authCacheMisses,
		Attempts:    authAttemptsTotal,
	}
}

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
	return func(ctx context.Context, req connect.AnyRequest) (resp connect.AnyResponse, err error) {
		method := sanitizeMethod(req.Spec().Procedure)
		start := time.Now()
		defer func() {
			code := "ok"
			if r := recover(); r != nil {
				code = "internal"
				rpcRequestsTotal.WithLabelValues(method, code).Inc()
				rpcRequestDuration.WithLabelValues(method).Observe(time.Since(start).Seconds())
				panic(r)
			}
			duration := time.Since(start).Seconds()
			if err != nil {
				code = connect.CodeOf(err).String()
			}
			rpcRequestsTotal.WithLabelValues(method, code).Inc()
			rpcRequestDuration.WithLabelValues(method).Observe(duration)
		}()
		resp, err = next(ctx, req)
		return
	}
}

func (m *metricsInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (m *metricsInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) (err error) {
		method := sanitizeMethod(conn.Spec().Procedure)
		rpcActiveStreams.Inc()
		start := time.Now()
		defer func() {
			rpcActiveStreams.Dec()
			code := "ok"
			if r := recover(); r != nil {
				code = "internal"
				rpcRequestsTotal.WithLabelValues(method, code).Inc()
				rpcStreamDuration.WithLabelValues(method).Observe(time.Since(start).Seconds())
				panic(r)
			}
			duration := time.Since(start).Seconds()
			if err != nil {
				code = connect.CodeOf(err).String()
			}
			rpcRequestsTotal.WithLabelValues(method, code).Inc()
			rpcStreamDuration.WithLabelValues(method).Observe(duration)
		}()
		err = next(ctx, conn)
		return
	}
}
