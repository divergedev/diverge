package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)
	return s
}

func newTestPreviewGroupReconciler(objs ...client.Object) (*PreviewGroupReconciler, client.Client) {
	s := newTestScheme()
	cb := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{})
	if len(objs) > 0 {
		cb = cb.WithObjects(objs...)
	}
	c := cb.Build()
	r := &PreviewGroupReconciler{
		Client:   c,
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
	}
	return r, c
}

func TestPreviewGroupReconcile_CreateChildEnvironments(t *testing.T) {
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
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{
					Name:      "payments-api",
					Image:     "registry.azra-ai.com/payments:mr-42",
					Mode:      divergeiov1alpha1.ServiceModeImage,
					Namespace: "product-rad",
					Port:      8080,
				},
				{
					Name:      "consent-mgr",
					Mode:      divergeiov1alpha1.ServiceModeBaseline,
					Namespace: "platform-core",
				},
			},
		},
	}

	r, c := newTestPreviewGroupReconciler(pg)

	// First reconcile: adds finalizer
	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-42"},
	})
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if result.Requeue || result.RequeueAfter > 0 {
		t.Log("Requeue after finalizer add — expected")
	}

	// Verify finalizer was added
	var updated divergeiov1alpha1.PreviewGroup
	if err := c.Get(context.Background(), types.NamespacedName{Name: "mr-42"}, &updated); err != nil {
		t.Fatalf("failed to get updated PreviewGroup: %v", err)
	}
	hasFinalizer := false
	for _, f := range updated.Finalizers {
		if f == previewGroupFinalizer {
			hasFinalizer = true
			break
		}
	}
	if !hasFinalizer {
		t.Error("finalizer not added")
	}

	// Second reconcile: creates child Environments
	result, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-42"},
	})
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	// Verify child Environment was created for payments-api (image mode)
	envName := childEnvironmentName("mr-42", "payments-api")
	var childEnv divergeiov1alpha1.Environment
	err = c.Get(context.Background(), types.NamespacedName{
		Name:      envName,
		Namespace: "product-rad",
	}, &childEnv)
	if err != nil {
		t.Fatalf("child Environment not created for payments-api: %v", err)
	}

	// Verify labels
	if childEnv.Labels[labelPreviewGroup] != "mr-42" {
		t.Errorf("child Environment missing preview-group label, got %q", childEnv.Labels[labelPreviewGroup])
	}

	// Verify routing config
	if childEnv.Spec.Routing.HeaderValue != "42" {
		t.Errorf("child Environment routing headerValue = %q, want %q", childEnv.Spec.Routing.HeaderValue, "42")
	}
	if childEnv.Spec.Routing.HeaderKey != "x-preview-env" {
		t.Errorf("child Environment routing headerKey = %q, want %q", childEnv.Spec.Routing.HeaderKey, "x-preview-env")
	}

	// Verify service config
	if childEnv.Spec.ServiceConfig == nil {
		t.Fatal("child Environment ServiceConfig is nil")
	}
	if childEnv.Spec.ServiceConfig.Image != "registry.azra-ai.com/payments:mr-42" {
		t.Errorf("child Environment image = %q, want %q", childEnv.Spec.ServiceConfig.Image, "registry.azra-ai.com/payments:mr-42")
	}
	if childEnv.Spec.ServiceConfig.Port != 8080 {
		t.Errorf("child Environment port = %d, want 8080", childEnv.Spec.ServiceConfig.Port)
	}

	// Verify NO child Environment for baseline service
	baselineEnvName := childEnvironmentName("mr-42", "consent-mgr")
	var baselineEnv divergeiov1alpha1.Environment
	err = c.Get(context.Background(), types.NamespacedName{
		Name:      baselineEnvName,
		Namespace: "platform-core",
	}, &baselineEnv)
	if err == nil {
		t.Error("child Environment should NOT be created for baseline service")
	}
}

func TestPreviewGroupReconcile_BaselinePhaseIsRunning(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "mr-99",
			Finalizers: []string{previewGroupFinalizer},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "azra/platform",
				Branch:   "main",
			},
			Routing: divergeiov1alpha1.PreviewGroupRouting{
				HeaderKey:   "x-preview-env",
				HeaderValue: "99",
			},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{
					Name:      "auth-svc",
					Mode:      divergeiov1alpha1.ServiceModeBaseline,
					Namespace: "platform-core",
				},
			},
		},
	}

	r, c := newTestPreviewGroupReconciler(pg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-99"},
	})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// Check status — baseline-only group should be Running
	var updated divergeiov1alpha1.PreviewGroup
	if err := c.Get(context.Background(), types.NamespacedName{Name: "mr-99"}, &updated); err != nil {
		t.Fatalf("failed to get updated PreviewGroup: %v", err)
	}
	if updated.Status.Phase != divergeiov1alpha1.PreviewGroupPhaseRunning {
		t.Errorf("phase = %q, want %q", updated.Status.Phase, divergeiov1alpha1.PreviewGroupPhaseRunning)
	}
	if updated.Status.ServiceCount != 1 {
		t.Errorf("serviceCount = %d, want 1", updated.Status.ServiceCount)
	}
}

func TestDerivePreviewGroupPhase(t *testing.T) {
	tests := []struct {
		name     string
		statuses []divergeiov1alpha1.PreviewGroupServiceStatus
		want     divergeiov1alpha1.PreviewGroupPhase
	}{
		{
			name:     "empty",
			statuses: nil,
			want:     divergeiov1alpha1.PreviewGroupPhasePending,
		},
		{
			name: "all running",
			statuses: []divergeiov1alpha1.PreviewGroupServiceStatus{
				{Phase: divergeiov1alpha1.PhaseRunning},
				{Phase: divergeiov1alpha1.PhaseRunning},
			},
			want: divergeiov1alpha1.PreviewGroupPhaseRunning,
		},
		{
			name: "all failed",
			statuses: []divergeiov1alpha1.PreviewGroupServiceStatus{
				{Phase: divergeiov1alpha1.PhaseFailed},
				{Phase: divergeiov1alpha1.PhaseFailed},
			},
			want: divergeiov1alpha1.PreviewGroupPhaseFailed,
		},
		{
			name: "mixed running + failed = degraded",
			statuses: []divergeiov1alpha1.PreviewGroupServiceStatus{
				{Phase: divergeiov1alpha1.PhaseRunning},
				{Phase: divergeiov1alpha1.PhaseFailed},
			},
			want: divergeiov1alpha1.PreviewGroupPhaseDegraded,
		},
		{
			name: "deploying",
			statuses: []divergeiov1alpha1.PreviewGroupServiceStatus{
				{Phase: divergeiov1alpha1.PhaseRunning},
				{Phase: divergeiov1alpha1.PhaseDeploying},
			},
			want: divergeiov1alpha1.PreviewGroupPhaseDeploying,
		},
		{
			name: "pending",
			statuses: []divergeiov1alpha1.PreviewGroupServiceStatus{
				{Phase: divergeiov1alpha1.PhasePending},
			},
			want: divergeiov1alpha1.PreviewGroupPhasePending,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := derivePreviewGroupPhase(tt.statuses)
			if got != tt.want {
				t.Errorf("derivePreviewGroupPhase() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChildEnvironmentName(t *testing.T) {
	tests := []struct {
		group   string
		service string
	}{
		{"mr-42", "payments-api"},
		{"mr-42", "consent-manager"},
		{"feature-very-long-branch-name-that-exceeds-limit", "service-with-long-name"},
	}

	for _, tt := range tests {
		t.Run(tt.group+"/"+tt.service, func(t *testing.T) {
			name := childEnvironmentName(tt.group, tt.service)
			if len(name) > 63 {
				t.Errorf("name too long: %d chars (max 63): %s", len(name), name)
			}
			if name == "" {
				t.Error("name is empty")
			}
			// Verify DNS-1123 label
			for _, c := range name {
				if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
					t.Errorf("invalid character %q in name %s", c, name)
				}
			}
		})
	}

	// Verify deterministic
	a := childEnvironmentName("mr-42", "payments-api")
	b := childEnvironmentName("mr-42", "payments-api")
	if a != b {
		t.Errorf("non-deterministic: %q != %q", a, b)
	}

	// Verify unique across different inputs
	x := childEnvironmentName("mr-42", "svc-a")
	y := childEnvironmentName("mr-42", "svc-b")
	if x == y {
		t.Errorf("collision: %q == %q", x, y)
	}
}
