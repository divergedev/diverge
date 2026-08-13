package cli

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestDevCmd(t *testing.T) {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)

	c := fake.NewClientBuilder().WithScheme(s).Build()
	app := &App{Client: c}

	// Intercept test
	cmd := newPreviewInterceptCmd(app)
	cmd.SetArgs([]string{"my-service", "--group", "dev-test", "--endpoint", "10.0.0.1:8080"})

	// Need a PreviewGroup first
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "dev-test"},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "my-service", Mode: divergeiov1alpha1.ServiceModeImage},
			},
		},
	}
	_ = c.Create(context.Background(), pg)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Intercept failed: %v", err)
	}

	_ = c.Get(context.Background(), types.NamespacedName{Name: "dev-test"}, pg)
	if pg.Spec.Services[0].Mode != divergeiov1alpha1.ServiceModeLocal {
		t.Errorf("Expected Local mode, got %v", pg.Spec.Services[0].Mode)
	}
	if pg.Spec.Services[0].Endpoint != "10.0.0.1:8080" {
		t.Errorf("Expected endpoint 10.0.0.1:8080, got %v", pg.Spec.Services[0].Endpoint)
	}

	// Release test
	releaseCmd := newPreviewReleaseCmd(app)
	releaseCmd.SetArgs([]string{"my-service", "--group", "dev-test"})
	err = releaseCmd.Execute()
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	_ = c.Get(context.Background(), types.NamespacedName{Name: "dev-test"}, pg)
	if pg.Spec.Services[0].Mode != divergeiov1alpha1.ServiceModeImage {
		t.Errorf("Expected Image mode, got %v", pg.Spec.Services[0].Mode)
	}
	if pg.Spec.Services[0].Endpoint != "" {
		t.Errorf("Expected empty endpoint, got %v", pg.Spec.Services[0].Endpoint)
	}
}
