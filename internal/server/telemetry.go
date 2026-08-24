package server

import (
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	requestsByProtocol = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "requests_by_protocol_total",
		Help:      "Total requests broken down by wire protocol",
	}, []string{"protocol"})

	requestsByClient = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "diverge",
		Subsystem: "server",
		Name:      "requests_by_client_total",
		Help:      "Total requests broken down by client SDK family",
	}, []string{"client"})
)

func init() {
	crmetrics.Registry.MustRegister(
		requestsByProtocol,
		requestsByClient,
	)
}

// detectProtocol maps Content-Type to ConnectRPC wire protocol.
func detectProtocol(contentType string) string {
	ct := strings.ToLower(strings.TrimSpace(contentType))

	// Strip parameters (e.g., "application/json; charset=utf-8")
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}

	switch ct {
	case "application/grpc", "application/grpc+proto":
		return "grpc"
	case "application/grpc-web", "application/grpc-web+proto", "application/grpc-web+json",
		"application/grpc-web-text", "application/grpc-web-text+proto":
		return "grpc-web"
	case "application/json":
		return "connect-json"
	case "application/proto":
		return "connect-proto"
	case "application/connect+json":
		return "connect-stream-json"
	case "application/connect+proto":
		return "connect-stream-proto"
	default:
		return "unknown"
	}
}

// detectClient maps User-Agent prefix to a known client SDK family.
func detectClient(ua string) string {
	ua = strings.ToLower(ua)
	switch {
	case strings.HasPrefix(ua, "connect-go/"):
		return "go-sdk"
	case strings.HasPrefix(ua, "connect-es/"):
		return "ts-sdk"
	case strings.HasPrefix(ua, "grpc-go/"):
		return "grpc-go"
	case strings.HasPrefix(ua, "grpc-web/"),
		strings.HasPrefix(ua, "grpc-web-javascript/"):
		return "grpc-web"
	case strings.HasPrefix(ua, "curl/"):
		return "curl"
	case strings.HasPrefix(ua, "buf/"):
		return "buf"
	default:
		return "other"
	}
}

// ProtocolTelemetryMiddleware records the ConnectRPC wire protocol and
// client SDK for every request. Designed for <1μs overhead per request.
func ProtocolTelemetryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip health/ready probes
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}

		protocol := detectProtocol(r.Header.Get("Content-Type"))
		requestsByProtocol.WithLabelValues(protocol).Inc()

		// Prefer X-User-Agent (gRPC-Web browser clients) over User-Agent.
		ua := r.Header.Get("X-User-Agent")
		if ua == "" {
			ua = r.Header.Get("User-Agent")
		}
		client := detectClient(ua)
		requestsByClient.WithLabelValues(client).Inc()

		next.ServeHTTP(w, r)
	})
}
