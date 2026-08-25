package deployer

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"pgregory.net/rapid"
)

func TestOTelAnnotationDeployer_InjectsAnnotations(t *testing.T) {
	inner := &NoopDeployer{}
	deployer := &OTelAnnotationDeployer{
		Inner: inner,
		Annotations: map[string]string{
			"instrumentation.opentelemetry.io/inject-java":   "true",
			"instrumentation.opentelemetry.io/inject-python": "true",
		},
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}

	if err := deployer.Deploy(context.Background(), env); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}

	if env.Annotations["instrumentation.opentelemetry.io/inject-java"] != "true" {
		t.Error("expected java annotation to be injected")
	}
	if env.Annotations["instrumentation.opentelemetry.io/inject-python"] != "true" {
		t.Error("expected python annotation to be injected")
	}
}

func TestOTelAnnotationDeployer_PreservesExistingAnnotations(t *testing.T) {
	inner := &NoopDeployer{}
	deployer := &OTelAnnotationDeployer{
		Inner: inner,
		Annotations: map[string]string{
			"instrumentation.opentelemetry.io/inject-java": "true",
		},
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-env",
			Namespace:   "default",
			Annotations: map[string]string{"existing": "value"},
		},
	}

	if err := deployer.Deploy(context.Background(), env); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}

	if env.Annotations["existing"] != "value" {
		t.Error("expected existing annotation to be preserved")
	}
	if env.Annotations["instrumentation.opentelemetry.io/inject-java"] != "true" {
		t.Error("expected java annotation to be injected")
	}
}

func TestOTelAnnotationDeployer_NoAnnotations(t *testing.T) {
	inner := &NoopDeployer{}
	deployer := &OTelAnnotationDeployer{
		Inner:       inner,
		Annotations: nil,
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}

	if err := deployer.Deploy(context.Background(), env); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}

	// No annotations should have been added
	if len(env.Annotations) != 0 {
		t.Errorf("expected no annotations, got %v", env.Annotations)
	}
}

func TestOTelAnnotationDeployer_Teardown(t *testing.T) {
	inner := &NoopDeployer{}
	deployer := &OTelAnnotationDeployer{Inner: inner}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env"},
	}

	if err := deployer.Teardown(context.Background(), env); err != nil {
		t.Fatalf("Teardown() error = %v", err)
	}
}

func TestOTelAnnotationDeployer_Status(t *testing.T) {
	inner := &NoopDeployer{}
	deployer := &OTelAnnotationDeployer{Inner: inner}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env"},
	}

	statuses, err := deployer.Status(context.Background(), env)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("expected empty statuses from noop, got %d", len(statuses))
	}
}

func TestOTelAnnotationDeployer_MergeAnnotations_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		existingMap := rapid.MapOf(
			rapid.StringMatching(`^[a-z0-9A-Z.-]+$`),
			rapid.String(),
		).Draw(t, "existingMap")

		otelMap := rapid.MapOf(
			rapid.StringMatching(`^instrumentation\.opentelemetry\.io/[a-z0-9-]+$`),
			rapid.String(),
		).Draw(t, "otelMap")

		inner := &NoopDeployer{}
		deployer := &OTelAnnotationDeployer{
			Inner:       inner,
			Annotations: otelMap,
		}

		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-env",
				Namespace: "default",
			},
		}

		if len(existingMap) > 0 {
			env.Annotations = make(map[string]string)
			for k, v := range existingMap {
				env.Annotations[k] = v
			}
		}

		err := deployer.Deploy(context.Background(), env)
		if err != nil {
			t.Fatalf("Deploy() error = %v", err)
		}

		// Verify all OTel annotations are present and take precedence
		for k, v := range otelMap {
			if env.Annotations[k] != v {
				t.Fatalf("expected otel annotation %s to be %s, got %s", k, v, env.Annotations[k])
			}
		}

		// Verify non-overlapping existing annotations are preserved
		for k, v := range existingMap {
			if _, ok := otelMap[k]; !ok {
				if env.Annotations[k] != v {
					t.Fatalf("expected existing annotation %s to be preserved as %s, got %s", k, v, env.Annotations[k])
				}
			}
		}
	})
}
