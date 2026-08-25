// Package observability wires OpenTelemetry for this service. It is the only
// place telemetry setup lives, and it follows the platform interface pattern:
// configuration comes exclusively from standard OTEL_* environment variables,
// export is OTLP, and no cloud SDK is ever imported. Where telemetry lands
// (Cloud Trace, Jaeger, anything else) is the collector's concern, not ours.
package observability

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Setup configures the global tracer provider and W3C trace-context
// propagation. It returns a shutdown function that flushes spans; call it on
// exit. When no OTLP endpoint is configured — neither the generic
// OTEL_EXPORTER_OTLP_ENDPOINT nor the signal-specific (and higher-precedence)
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT — tracing stays a no-op and the returned
// shutdown does nothing.
func Setup(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" &&
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, err
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}
