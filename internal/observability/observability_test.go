package observability

import (
	"context"
	"os"
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
}

func TestSetup_AnyServiceName_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		serviceName := rapid.StringMatching(`.+`).Draw(t, "serviceName")

		_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		_ = os.Unsetenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")

		shutdown, err := Setup(context.Background(), serviceName)
		if err != nil {
			t.Fatalf("unexpected error for serviceName %q: %v", serviceName, err)
		}
		if shutdown == nil {
			t.Fatalf("expected non-nil shutdown for serviceName %q", serviceName)
		}
	})
}
