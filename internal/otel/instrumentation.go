package otel

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// InstrumentationConfig holds configuration for creating Instrumentation CRs.
type InstrumentationConfig struct {
	OTLPEndpoint    string
	EnvName         string
	PreviewGroup    string
	BaselineVersion string
	GoEnabled       bool // default: false (eBPF needs privileges)
}

// EnsureInstrumentation creates or updates an Instrumentation CR in the given namespace.
func EnsureInstrumentation(ctx context.Context, c client.Client, namespace string, apiVersion string, cfg InstrumentationConfig) error {
	instr := &unstructured.Unstructured{}

	// e.g. apiVersion: "opentelemetry.io/v1alpha2"
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return fmt.Errorf("invalid apiVersion %q: %w", apiVersion, err)
	}
	instr.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   gv.Group,
		Version: gv.Version,
		Kind:    "Instrumentation",
	})
	instr.SetName("diverge-auto-instrumentation")
	instr.SetNamespace(namespace)

	// Resource attributes
	envAttributes := []interface{}{
		map[string]interface{}{"name": "diverge.environment.name", "value": cfg.EnvName},
	}
	if cfg.PreviewGroup != "" {
		envAttributes = append(envAttributes, map[string]interface{}{"name": "diverge.preview_group.name", "value": cfg.PreviewGroup})
	}
	if cfg.BaselineVersion != "" {
		envAttributes = append(envAttributes, map[string]interface{}{"name": "diverge.baseline.version", "value": cfg.BaselineVersion})
	}

	spec := map[string]interface{}{
		"exporter": map[string]interface{}{
			"endpoint": cfg.OTLPEndpoint,
		},
		"propagators": []interface{}{"tracecontext", "baggage"},
		"env":         envAttributes,
		"java":        map[string]interface{}{},
		"nodejs":      map[string]interface{}{},
		"python":      map[string]interface{}{},
		"dotnet":      map[string]interface{}{},
	}

	if cfg.GoEnabled {
		spec["go"] = map[string]interface{}{}
	}

	instr.Object["spec"] = spec

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(instr.GroupVersionKind())
	err = c.Get(ctx, client.ObjectKey{Name: instr.GetName(), Namespace: instr.GetNamespace()}, existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if err := c.Create(ctx, instr); err != nil {
				return fmt.Errorf("failed to create Instrumentation CR: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to check existing Instrumentation CR: %w", err)
	}

	// For update, we need resourceVersion
	instr.SetResourceVersion(existing.GetResourceVersion())
	if err := c.Update(ctx, instr); err != nil {
		return fmt.Errorf("failed to update Instrumentation CR: %w", err)
	}
	return nil
}

// DeleteInstrumentation removes the Instrumentation CR from the namespace.
func DeleteInstrumentation(ctx context.Context, c client.Client, namespace string, apiVersion string) error {
	instr := &unstructured.Unstructured{}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return fmt.Errorf("invalid apiVersion %q: %w", apiVersion, err)
	}
	instr.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   gv.Group,
		Version: gv.Version,
		Kind:    "Instrumentation",
	})
	instr.SetName("diverge-auto-instrumentation")
	instr.SetNamespace(namespace)

	if err := c.Delete(ctx, instr); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete Instrumentation CR: %w", err)
	}
	return nil
}
