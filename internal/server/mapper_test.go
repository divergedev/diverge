package server

import (
	"testing"
	"time"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEnvironmentMapper_Roundtrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	original := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-env",
			Namespace:         "default",
			Labels:            map[string]string{"app": "test"},
			CreationTimestamp: metav1.NewTime(now),
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "org/repo",
				Branch:   "main",
			},
			Testing: &v1alpha1.TestingSpec{
				Enabled: true,
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			Phase: v1alpha1.PhaseRunning,
			URL:   "https://test.example.com",
			Conditions: []metav1.Condition{
				{
					Type:   "Ready",
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	proto := EnvironmentToProto(original)
	if proto == nil {
		t.Fatal("expected non-nil proto")
	}

	crd := ProtoToEnvironment(proto)
	if crd == nil {
		t.Fatal("expected non-nil crd")
	}

	// Compare with cmp, ignoring some unexported fields in metav1.Time
	opts := []cmp.Option{
		cmpopts.IgnoreUnexported(metav1.Time{}),
	}
	if diff := cmp.Diff(original, crd, opts...); diff != "" {
		t.Errorf("Roundtrip mismatch (-want +got):\n%s", diff)
	}
}

func TestPreviewGroupMapper_Roundtrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	original := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pg",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(now),
		},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
			},
			Routing: v1alpha1.PreviewGroupRouting{
				HeaderValue: "123",
			},
			Services: []v1alpha1.PreviewGroupServiceSpec{
				{
					Name: "svc1",
					Mode: v1alpha1.ServiceModeImage,
				},
			},
		},
	}

	proto := PreviewGroupToProto(original)
	if proto == nil {
		t.Fatal("expected non-nil proto")
	}

	crd := ProtoToPreviewGroup(proto)
	if crd == nil {
		t.Fatal("expected non-nil crd")
	}

	opts := []cmp.Option{
		cmpopts.IgnoreUnexported(metav1.Time{}),
	}
	if diff := cmp.Diff(original, crd, opts...); diff != "" {
		t.Errorf("Roundtrip mismatch (-want +got):\n%s", diff)
	}
}

func TestMappers_NilSafety(t *testing.T) {
	if got := EnvironmentToProto(nil); got != nil {
		t.Errorf("EnvironmentToProto(nil) = %v, want nil", got)
	}
	if got := ProtoToEnvironment(nil); got != nil {
		t.Errorf("ProtoToEnvironment(nil) = %v, want nil", got)
	}
	if got := PreviewGroupToProto(nil); got != nil {
		t.Errorf("PreviewGroupToProto(nil) = %v, want nil", got)
	}
	if got := ProtoToPreviewGroup(nil); got != nil {
		t.Errorf("ProtoToPreviewGroup(nil) = %v, want nil", got)
	}

	// Test nested nil safety
	pbEnv := &pb.Environment{}
	crdEnv := ProtoToEnvironment(pbEnv)
	if crdEnv.Spec.Testing != nil {
		t.Errorf("expected nil testing spec, got %v", crdEnv.Spec.Testing)
	}
}
