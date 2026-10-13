package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

var w3cTraceparentPattern = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)

func TestResolveTraceparent(t *testing.T) {
	t.Run("extracts valid traceparent header", func(t *testing.T) {
		expected := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		req := httptest.NewRequest(http.MethodPost, "/github-webhook", nil)
		req.Header.Set("traceparent", expected)

		got := ResolveTraceparent(req)
		assert.Equal(t, expected, got)
	})

	t.Run("extracts valid X-Traceparent fallback header", func(t *testing.T) {
		expected := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		req := httptest.NewRequest(http.MethodPost, "/gitlab-webhook", nil)
		req.Header.Set("X-Traceparent", expected)

		got := ResolveTraceparent(req)
		assert.Equal(t, expected, got)
	})

	t.Run("extracts from active OpenTelemetry SpanContext when header absent", func(t *testing.T) {
		traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
		require.NoError(t, err)
		spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
		require.NoError(t, err)
		sc := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
		})
		ctx := trace.ContextWithSpanContext(context.Background(), sc)
		req := httptest.NewRequest(http.MethodPost, "/github-webhook", nil).WithContext(ctx)

		got := ResolveTraceparent(req)
		assert.Equal(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", got)
	})

	t.Run("rejects invalid traceparent header and falls back to active span in context", func(t *testing.T) {
		traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
		require.NoError(t, err)
		spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
		require.NoError(t, err)
		sc := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
		})
		ctx := trace.ContextWithSpanContext(context.Background(), sc)
		req := httptest.NewRequest(http.MethodPost, "/github-webhook", nil).WithContext(ctx)
		req.Header.Set("traceparent", "not-a-valid-traceparent")

		got := ResolveTraceparent(req)
		assert.Equal(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", got)
		assert.False(t, isValidTraceparent(ctx, "not-a-valid-traceparent"))
	})

	t.Run("generates fresh W3C traceparent when header is missing or invalid", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/github-webhook", nil)
		req.Header.Set("traceparent", "invalid-traceparent-value")

		got1 := ResolveTraceparent(req)
		got2 := ResolveTraceparent(nil)

		assert.Regexp(t, w3cTraceparentPattern, got1)
		assert.Regexp(t, w3cTraceparentPattern, got2)
		assert.NotEqual(t, got1, got2)
		assert.True(t, isValidTraceparent(context.Background(), got1))
	})
}

func TestSetTraceparentAnnotation(t *testing.T) {
	t.Run("nil object or empty traceparent is a no-op", func(t *testing.T) {
		SetTraceparentAnnotation(nil, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")

		env := &v1alpha1.Environment{}
		SetTraceparentAnnotation(env, "   ")
		assert.Empty(t, env.GetAnnotations())
	})

	t.Run("initializes annotations map and sets divergedev.com/traceparent", func(t *testing.T) {
		tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		env := &v1alpha1.Environment{}
		SetTraceparentAnnotation(env, tp)
		assert.Equal(t, tp, env.Annotations[TraceparentAnnotation])
	})
}
