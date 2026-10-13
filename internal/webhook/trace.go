package webhook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TraceparentAnnotation is the Kubernetes annotation key used to propagate
// W3C Trace Context across Diverge custom resources and child workloads.
const TraceparentAnnotation = "divergedev.com/traceparent"

type traceparentContextKey struct{}

// WithTraceparent returns a copy of ctx carrying the resolved W3C traceparent string.
func WithTraceparent(ctx context.Context, traceparent string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, traceparentContextKey{}, strings.TrimSpace(traceparent))
}

// TraceparentFromContext returns the W3C traceparent stored in ctx, or resolves/generates one if absent.
func TraceparentFromContext(ctx context.Context) string {
	if ctx != nil {
		if tp, ok := ctx.Value(traceparentContextKey{}).(string); ok && strings.TrimSpace(tp) != "" {
			return strings.TrimSpace(tp)
		}
		if formatted := formatTraceparentFromContext(ctx); formatted != "" {
			return formatted
		}
	}
	return generateTraceparent()
}

// ResolveTraceparent extracts or generates a valid W3C version-00 traceparent string
// for an incoming webhook HTTP request.
func ResolveTraceparent(r *http.Request) string {
	if r != nil {
		baseCtx := r.Context()
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		for _, headerName := range []string{"traceparent", "X-Traceparent"} {
			tp := strings.TrimSpace(r.Header.Get(headerName))
			if tp != "" && isValidTraceparent(baseCtx, tp) {
				return strings.ToLower(tp)
			}
		}
		if formatted := formatTraceparentFromContext(baseCtx); formatted != "" {
			return formatted
		}
	}
	return generateTraceparent()
}

// SetTraceparentAnnotation sets the divergedev.com/traceparent annotation on obj
// when obj is non-nil and traceparent is non-empty.
func SetTraceparentAnnotation(obj metav1.Object, traceparent string) {
	tp := strings.TrimSpace(traceparent)
	if obj == nil || tp == "" {
		return
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[TraceparentAnnotation] = tp
	obj.SetAnnotations(annotations)
}

func isValidTraceparent(_ context.Context, tp string) bool {
	extracted := (propagation.TraceContext{}).Extract(context.Background(), propagation.MapCarrier{
		"traceparent": strings.ToLower(strings.TrimSpace(tp)),
	})
	return trace.SpanContextFromContext(extracted).IsValid()
}

func formatTraceparentFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	carrier := propagation.MapCarrier{}
	(propagation.TraceContext{}).Inject(ctx, carrier)
	return strings.TrimSpace(carrier["traceparent"])
}

func generateTraceparent() string {
	var traceID [16]byte
	var spanID [8]byte
	if _, err := rand.Read(traceID[:]); err != nil || isAllZero(traceID[:]) {
		traceID[15] = 1
	}
	if _, err := rand.Read(spanID[:]); err != nil || isAllZero(spanID[:]) {
		spanID[7] = 1
	}
	return fmt.Sprintf("00-%s-%s-01", hex.EncodeToString(traceID[:]), hex.EncodeToString(spanID[:]))
}

func isAllZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
