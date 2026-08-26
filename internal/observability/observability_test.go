package observability

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"pgregory.net/rapid"
)

func TestSetup_NoopWhenUnconfigured(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")

	shutdown, err := Setup(context.Background(), "test-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown function")
	}
}

func TestSetup_ShutdownIsCallable(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")

	shutdown, _ := Setup(context.Background(), "test-service")
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("unexpected error calling shutdown: %v", err)
	}
}

func TestSetup_PropagatorAlwaysSet(t *testing.T) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")

	_, err := Setup(context.Background(), "test-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prop := otel.GetTextMapPropagator()
	if prop == nil {
		t.Fatal("expected text map propagator to be set")
	}

	// Verify both TraceContext and Baggage propagators are configured
	inCarrier := propagation.MapCarrier{
		"traceparent": "00-00000000000000000000000000000001-0000000000000001-01",
		"baggage":     "key=value",
	}

	// Extract using the configured global propagator
	ctx := prop.Extract(context.Background(), inCarrier)

	tc := propagation.TraceContext{}
	bg := propagation.Baggage{}

	outCarrierTC := propagation.MapCarrier{}
	tc.Inject(ctx, outCarrierTC)
	if outCarrierTC["traceparent"] != inCarrier["traceparent"] {
		t.Errorf("TraceContext missing or incorrect: got %v", outCarrierTC["traceparent"])
	}

	outCarrierBG := propagation.MapCarrier{}
	bg.Inject(ctx, outCarrierBG)
	if outCarrierBG["baggage"] != inCarrier["baggage"] {
		t.Errorf("Baggage missing or incorrect: got %v", outCarrierBG["baggage"])
	}
}

func TestSetup_AnyServiceName_PBT(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")

	rapid.Check(t, func(t *rapid.T) {
		serviceName := rapid.StringMatching(`.+`).Draw(t, "serviceName")

		shutdown, err := Setup(context.Background(), serviceName)
		if err != nil {
			t.Fatalf("unexpected error for serviceName %q: %v", serviceName, err)
		}
		if shutdown == nil {
			t.Fatalf("expected non-nil shutdown for serviceName %q", serviceName)
		}
	})
}
