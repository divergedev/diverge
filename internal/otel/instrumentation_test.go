package otel

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"pgregory.net/rapid"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestEnsureInstrumentation_CreatesWhenCRDExists(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	cfg := InstrumentationConfig{
		OTLPEndpoint: "http://otel:4317",
		EnvName:      "test-env",
	}

	err := EnsureInstrumentation(context.Background(), c, "default", "opentelemetry.io/v1alpha2", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	instr := &unstructured.Unstructured{}
	instr.SetGroupVersionKind(schema.GroupVersionKind{Group: "opentelemetry.io", Version: "v1alpha2", Kind: "Instrumentation"})
	err = c.Get(context.Background(), client.ObjectKey{Name: "diverge-auto-instrumentation", Namespace: "default"}, instr)
	if err != nil {
		t.Fatalf("failed to get created CR: %v", err)
	}
}

func TestEnsureInstrumentation_ResourceAttributes(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	cfg := InstrumentationConfig{
		OTLPEndpoint:    "http://otel:4317",
		EnvName:         "test-env",
		PreviewGroup:    "test-group",
		BaselineVersion: "v1",
	}

	if err := EnsureInstrumentation(context.Background(), c, "default", "opentelemetry.io/v1alpha2", cfg); err != nil {
		t.Fatalf("EnsureInstrumentation failed: %v", err)
	}

	instr := &unstructured.Unstructured{}
	instr.SetGroupVersionKind(schema.GroupVersionKind{Group: "opentelemetry.io", Version: "v1alpha2", Kind: "Instrumentation"})
	if err := c.Get(context.Background(), client.ObjectKey{Name: "diverge-auto-instrumentation", Namespace: "default"}, instr); err != nil {
		t.Fatalf("failed to get CR: %v", err)
	}

	resAttrs, found, _ := unstructured.NestedStringMap(instr.Object, "spec", "resource", "resourceAttributes")
	if !found {
		t.Fatal("expected resource attributes in spec.resource.resourceAttributes")
	}
	if len(resAttrs) != 3 {
		t.Fatalf("expected 3 resource attributes, got %d", len(resAttrs))
	}
	if resAttrs["diverge.environment.name"] != "test-env" {
		t.Fatalf("expected diverge.environment.name=test-env, got %q", resAttrs["diverge.environment.name"])
	}
	if resAttrs["diverge.preview_group.name"] != "test-group" {
		t.Fatalf("expected diverge.preview_group.name=test-group, got %q", resAttrs["diverge.preview_group.name"])
	}
	if resAttrs["diverge.baseline.version"] != "v1" {
		t.Fatalf("expected diverge.baseline.version=v1, got %q", resAttrs["diverge.baseline.version"])
	}
}

func TestEnsureInstrumentation_GoDisabledByDefault(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	cfg := InstrumentationConfig{}
	if err := EnsureInstrumentation(context.Background(), c, "default", "opentelemetry.io/v1alpha2", cfg); err != nil {
		t.Fatalf("EnsureInstrumentation failed: %v", err)
	}

	instr := &unstructured.Unstructured{}
	instr.SetGroupVersionKind(schema.GroupVersionKind{Group: "opentelemetry.io", Version: "v1alpha2", Kind: "Instrumentation"})
	if err := c.Get(context.Background(), client.ObjectKey{Name: "diverge-auto-instrumentation", Namespace: "default"}, instr); err != nil {
		t.Fatalf("failed to get CR: %v", err)
	}

	_, found, _ := unstructured.NestedMap(instr.Object, "spec", "go")
	if found {
		t.Fatal("expected go section to be absent")
	}
}

func TestEnsureInstrumentation_GoEnabledWhenSet(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	cfg := InstrumentationConfig{GoEnabled: true}
	if err := EnsureInstrumentation(context.Background(), c, "default", "opentelemetry.io/v1alpha2", cfg); err != nil {
		t.Fatalf("EnsureInstrumentation failed: %v", err)
	}

	instr := &unstructured.Unstructured{}
	instr.SetGroupVersionKind(schema.GroupVersionKind{Group: "opentelemetry.io", Version: "v1alpha2", Kind: "Instrumentation"})
	if err := c.Get(context.Background(), client.ObjectKey{Name: "diverge-auto-instrumentation", Namespace: "default"}, instr); err != nil {
		t.Fatalf("failed to get CR: %v", err)
	}

	_, found, _ := unstructured.NestedMap(instr.Object, "spec", "go")
	if !found {
		t.Fatal("expected go section to be present")
	}
}

func TestDeleteInstrumentation_CleansUp(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	cfg := InstrumentationConfig{}
	if err := EnsureInstrumentation(context.Background(), c, "default", "opentelemetry.io/v1alpha2", cfg); err != nil {
		t.Fatalf("EnsureInstrumentation failed: %v", err)
	}

	err := DeleteInstrumentation(context.Background(), c, "default", "opentelemetry.io/v1alpha2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	instr := &unstructured.Unstructured{}
	instr.SetGroupVersionKind(schema.GroupVersionKind{Group: "opentelemetry.io", Version: "v1alpha2", Kind: "Instrumentation"})
	err = c.Get(context.Background(), client.ObjectKey{Name: "diverge-auto-instrumentation", Namespace: "default"}, instr)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound error, got %v", err)
	}
}

func TestEnsureInstrumentation_AnyEnvName_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		c := fake.NewClientBuilder().Build()
		envName := rapid.String().Draw(t, "envName")
		previewGroup := rapid.String().Draw(t, "previewGroup")

		cfg := InstrumentationConfig{
			EnvName:      envName,
			PreviewGroup: previewGroup,
		}

		err := EnsureInstrumentation(context.Background(), c, "default", "opentelemetry.io/v1alpha2", cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		instr := &unstructured.Unstructured{}
		instr.SetGroupVersionKind(schema.GroupVersionKind{Group: "opentelemetry.io", Version: "v1alpha2", Kind: "Instrumentation"})
		if err := c.Get(context.Background(), client.ObjectKey{Name: "diverge-auto-instrumentation", Namespace: "default"}, instr); err != nil {
			t.Fatalf("failed to get CR: %v", err)
		}

		resAttrs, _, _ := unstructured.NestedStringMap(instr.Object, "spec", "resource", "resourceAttributes")

		if resAttrs["diverge.environment.name"] != envName {
			t.Fatalf("expected diverge.environment.name=%q, got %q", envName, resAttrs["diverge.environment.name"])
		}
		if previewGroup != "" && resAttrs["diverge.preview_group.name"] != previewGroup {
			t.Fatalf("expected diverge.preview_group.name=%q, got %q", previewGroup, resAttrs["diverge.preview_group.name"])
		}
	})
}
