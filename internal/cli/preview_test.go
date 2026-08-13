package cli

import (
	"bytes"
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestParseServiceSpecs(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		want    []divergeiov1alpha1.PreviewGroupServiceSpec
		wantErr bool
	}{
		{
			name:  "baseline only",
			input: []string{"consent-mgr"},
			want: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "consent-mgr", Mode: divergeiov1alpha1.ServiceModeBaseline},
			},
		},
		{
			name:  "image with tag",
			input: []string{"payments-api=registry.azra-ai.com/payments:mr-42"},
			want: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "payments-api", Mode: divergeiov1alpha1.ServiceModeImage, Image: "registry.azra-ai.com/payments:mr-42", Port: 8080},
			},
		},
		{
			name:  "image with tag and port",
			input: []string{"payments-api=registry.azra-ai.com/payments:mr-42:9090"},
			want: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "payments-api", Mode: divergeiov1alpha1.ServiceModeImage, Image: "registry.azra-ai.com/payments:mr-42", Port: 9090},
			},
		},
		{
			name:  "mixed services",
			input: []string{"payments-api=img:8080", "consent-mgr", "auth-svc=img2:9090"},
			want: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "payments-api", Mode: divergeiov1alpha1.ServiceModeImage, Image: "img", Port: 8080},
				{Name: "consent-mgr", Mode: divergeiov1alpha1.ServiceModeBaseline},
				{Name: "auth-svc", Mode: divergeiov1alpha1.ServiceModeImage, Image: "img2", Port: 9090},
			},
		},
		{
			name:  "registry with port",
			input: []string{"payments-api=registry.example.com:5000/payments"},
			want: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "payments-api", Mode: divergeiov1alpha1.ServiceModeImage, Image: "registry.example.com:5000/payments", Port: 8080},
			},
		},
		{
			name:  "not a port",
			input: []string{"name=img:8080abc"},
			want: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "name", Mode: divergeiov1alpha1.ServiceModeImage, Image: "img:8080abc", Port: 8080},
			},
		},
		{
			name:    "empty name",
			input:   []string{"=image"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			specs, err := parseServiceSpecs(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(specs) != len(tt.want) {
				t.Errorf("got %d specs, want %d", len(specs), len(tt.want))
			} else {
				for i := range specs {
					if specs[i].Name != tt.want[i].Name || specs[i].Mode != tt.want[i].Mode || specs[i].Image != tt.want[i].Image || specs[i].Port != tt.want[i].Port {
						t.Errorf("spec[%d] = %+v, want %+v", i, specs[i], tt.want[i])
					}
				}
			}
		})
	}
}

func TestParseServiceSpecs_Modes(t *testing.T) {
	specs, err := parseServiceSpecs([]string{"payments-api=img:8080", "consent-mgr"})
	if err != nil {
		t.Fatal(err)
	}

	if specs[0].Mode != divergeiov1alpha1.ServiceModeImage {
		t.Errorf("first spec mode = %q, want image", specs[0].Mode)
	}
	if specs[0].Port != 8080 {
		t.Errorf("first spec port = %d, want 8080", specs[0].Port)
	}
	if specs[0].Image != "img" {
		t.Errorf("first spec image = %q, want img", specs[0].Image)
	}

	if specs[1].Mode != divergeiov1alpha1.ServiceModeBaseline {
		t.Errorf("second spec mode = %q, want baseline", specs[1].Mode)
	}
}

func TestPreviewStatusCmd(t *testing.T) {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)

	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mr-42",
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "azra/platform",
				Branch:   "feat/payments",
			},
			Routing: divergeiov1alpha1.PreviewGroupRouting{
				HeaderKey:   "x-preview-env",
				HeaderValue: "42",
			},
		},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			Phase:        divergeiov1alpha1.PreviewGroupPhaseRunning,
			ServiceCount: 2,
			Services: []divergeiov1alpha1.PreviewGroupServiceStatus{
				{Name: "payments-api", Phase: divergeiov1alpha1.PhaseRunning, Namespace: "product-rad"},
				{Name: "consent-mgr", Phase: divergeiov1alpha1.PhaseRunning, Namespace: "platform-core"},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(pg).Build()
	app := &App{Client: c}

	var buf bytes.Buffer
	err := runPreviewStatus(app, "mr-42", &buf)
	output := buf.String()

	if err != nil {
		t.Fatalf("runPreviewStatus failed: %v", err)
	}
	if !bytes.Contains([]byte(output), []byte("mr-42")) {
		t.Error("output doesn't contain PreviewGroup name")
	}
	if !bytes.Contains([]byte(output), []byte("payments-api")) {
		t.Error("output doesn't contain service name")
	}
}

func TestPreviewDeleteCmd_NotFound(t *testing.T) {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)

	c := fake.NewClientBuilder().WithScheme(s).Build()
	app := &App{Client: c}

	err := runPreviewDelete(app, "nonexistent", true)
	if err == nil {
		t.Error("expected error for non-existent PreviewGroup")
	}
}

func TestPreviewDeleteCmd_Force(t *testing.T) {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)

	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mr-42",
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(pg).Build()
	app := &App{Client: c}

	err := runPreviewDelete(app, "mr-42", true)
	if err != nil {
		t.Fatalf("force delete failed: %v", err)
	}

	// Verify deleted
	var deleted divergeiov1alpha1.PreviewGroup
	err = c.Get(context.Background(), types.NamespacedName{Name: "mr-42"}, &deleted)
	if err == nil {
		t.Error("PreviewGroup still exists after delete")
	}
}

func TestPhaseEmoji(t *testing.T) {
	tests := []struct {
		phase string
		want  string
	}{
		{string(divergeiov1alpha1.PreviewGroupPhaseRunning), "✅"},
		{string(divergeiov1alpha1.PreviewGroupPhaseFailed), "❌"},
		{string(divergeiov1alpha1.PreviewGroupPhaseDegraded), "⚠️"},
		{string(divergeiov1alpha1.PreviewGroupPhaseDeploying), "🔄"},
		{string(divergeiov1alpha1.PreviewGroupPhasePending), "⏳"},
		{"", "⏳"},
	}

	for _, tt := range tests {
		got := phaseEmoji(tt.phase)
		if got != tt.want {
			t.Errorf("phaseEmoji(%q) = %q, want %q", tt.phase, got, tt.want)
		}
	}
}
