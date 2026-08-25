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

	envAttrs, found, _ := unstructured.NestedSlice(instr.Object, "spec", "env")
	if !found {
		t.Fatal("expected env attributes in spec")
	}
	if len(envAttrs) != 3 {
		t.Fatalf("expected 3 env attributes, got %d", len(envAttrs))
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

		envAttrs, _, _ := unstructured.NestedSlice(instr.Object, "spec", "env")
		foundEnv := false
		foundGroup := false
		for _, attr := range envAttrs {
			m := attr.(map[string]interface{})
			if m["name"] == "diverge.environment.name" && m["value"] == envName {
				foundEnv = true
			}
			if m["name"] == "diverge.preview_group.name" && m["value"] == previewGroup {
				foundGroup = true
			}
		}

		if !foundEnv {
			t.Fatalf("expected diverge.environment.name to be %q", envName)
		}
		if previewGroup != "" && !foundGroup {
			t.Fatalf("expected diverge.preview_group.name to be %q", previewGroup)
		}
	})
}
