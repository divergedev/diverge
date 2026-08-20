package server

import (
	"testing"
	"time"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCRDEnvToProto_Nil(t *testing.T) {
	proto, err := CRDEnvToProto(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proto != nil {
		t.Fatalf("expected nil, got %v", proto)
	}
}

func TestProtoEnvToCRD_Nil(t *testing.T) {
	crd, err := ProtoEnvToCRD(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if crd != nil {
		t.Fatalf("expected nil, got %v", crd)
	}
}

func TestCRDPgToProto_Nil(t *testing.T) {
	proto, err := CRDPgToProto(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proto != nil {
		t.Fatalf("expected nil, got %v", proto)
	}
}

func TestProtoPgToCRD_Nil(t *testing.T) {
	crd, err := ProtoPgToCRD(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if crd != nil {
		t.Fatalf("expected nil, got %v", crd)
	}
}

func TestEnvironment_RoundTrip_CRD_To_Proto_To_CRD(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-env",
			Namespace:         "test-ns",
			ResourceVersion:   "12345",
			CreationTimestamp: metav1.NewTime(now),
			Labels:            map[string]string{"env": "prod"},
			Annotations:       map[string]string{"foo": "bar"},
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Mode: "delta",
			},
			Routing: v1alpha1.EnvironmentRouting{
				HeaderKey: "x-env",
			},
		},
	}

	proto, err := CRDEnvToProto(original)
	if err != nil {
		t.Fatalf("CRDEnvToProto failed: %v", err)
	}

	if proto.Name != "test-env" {
		t.Errorf("expected proto.Name to be 'test-env', got '%s'", proto.Name)
	}
	if proto.ResourceVersion != "12345" {
		t.Errorf("expected proto.ResourceVersion to be '12345', got '%s'", proto.ResourceVersion)
	}
	if proto.CreatedAt == nil || !proto.CreatedAt.AsTime().Equal(now) {
		t.Errorf("expected proto.CreatedAt to be %v, got %v", now, proto.CreatedAt)
	}

	crd, err := ProtoEnvToCRD(proto)
	if err != nil {
		t.Fatalf("ProtoEnvToCRD failed: %v", err)
	}

	if crd.Name != "test-env" {
		t.Errorf("expected crd.Name to be 'test-env', got '%s'", crd.Name)
	}
	if crd.Namespace != "test-ns" {
		t.Errorf("expected crd.Namespace to be 'test-ns', got '%s'", crd.Namespace)
	}
	if crd.ResourceVersion != "12345" {
		t.Errorf("expected crd.ResourceVersion to be '12345', got '%s'", crd.ResourceVersion)
	}
	if crd.Labels["env"] != "prod" {
		t.Errorf("expected crd.Labels['env'] to be 'prod', got '%s'", crd.Labels["env"])
	}
	if crd.Annotations["foo"] != "bar" {
		t.Errorf("expected crd.Annotations['foo'] to be 'bar', got '%s'", crd.Annotations["foo"])
	}
	if crd.Spec.Deploy.Mode != "delta" {
		t.Errorf("expected Spec.Deploy.Mode to be 'delta', got '%s'", crd.Spec.Deploy.Mode)
	}
	if crd.Spec.Routing.HeaderKey != "x-env" {
		t.Errorf("expected Spec.Routing.HeaderKey to be 'x-env', got '%s'", crd.Spec.Routing.HeaderKey)
	}
	if !crd.CreationTimestamp.Time.Equal(now) {
		t.Errorf("expected crd.CreationTimestamp to be %v, got %v", now, crd.CreationTimestamp)
	}
}

func TestEnvironment_RoundTrip_Proto_To_CRD_To_Proto(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := &pb.Environment{
		Name:            "test-env",
		Namespace:       "test-ns",
		ResourceVersion: "12345",
		CreatedAt:       timestamppb.New(now),
		Labels:          map[string]string{"env": "prod"},
		Annotations:     map[string]string{"foo": "bar"},
		Spec: &pb.EnvironmentSpec{
			Deploy: &pb.EnvironmentDeploy{
				Mode: "delta",
			},
			Routing: &pb.EnvironmentRouting{
				HeaderKey: "x-env",
			},
		},
	}

	crd, err := ProtoEnvToCRD(original)
	if err != nil {
		t.Fatalf("ProtoEnvToCRD failed: %v", err)
	}
	if crd.Name != "test-env" {
		t.Errorf("expected crd.Name to be 'test-env', got '%s'", crd.Name)
	}
	if !crd.CreationTimestamp.Time.Equal(now) {
		t.Errorf("expected crd.CreationTimestamp to be %v, got %v", now, crd.CreationTimestamp)
	}

	proto, err := CRDEnvToProto(crd)
	if err != nil {
		t.Fatalf("CRDEnvToProto failed: %v", err)
	}

	if proto.Name != "test-env" {
		t.Errorf("expected proto.Name to be 'test-env', got '%s'", proto.Name)
	}
	if proto.ResourceVersion != "12345" {
		t.Errorf("expected proto.ResourceVersion to be '12345', got '%s'", proto.ResourceVersion)
	}
	if proto.Labels["env"] != "prod" {
		t.Errorf("expected proto.Labels['env'] to be 'prod', got '%s'", proto.Labels["env"])
	}
	if proto.Annotations["foo"] != "bar" {
		t.Errorf("expected proto.Annotations['foo'] to be 'bar', got '%s'", proto.Annotations["foo"])
	}
	if proto.Spec.Deploy.Mode != "delta" {
		t.Errorf("expected Spec.Deploy.Mode to be 'delta', got '%s'", proto.Spec.Deploy.Mode)
	}
	if proto.Spec.Routing.HeaderKey != "x-env" {
		t.Errorf("expected Spec.Routing.HeaderKey to be 'x-env', got '%s'", proto.Spec.Routing.HeaderKey)
	}
	if proto.CreatedAt == nil || !proto.CreatedAt.AsTime().Equal(now) {
		t.Errorf("expected proto.CreatedAt to be %v, got %v", now, proto.CreatedAt)
	}
}

func TestPreviewGroup_RoundTrip_CRD_To_Proto_To_CRD(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pg",
			Namespace:         "test-ns",
			ResourceVersion:   "12345",
			CreationTimestamp: metav1.NewTime(now),
			Labels:            map[string]string{"env": "prod"},
			Annotations:       map[string]string{"foo": "bar"},
		},
		Spec: v1alpha1.PreviewGroupSpec{
			// Add dummy fields if necessary, currently tests without
		},
	}

	proto, err := CRDPgToProto(original)
	if err != nil {
		t.Fatalf("CRDPgToProto failed: %v", err)
	}

	if proto.Name != "test-pg" {
		t.Errorf("expected proto.Name to be 'test-pg', got '%s'", proto.Name)
	}
	if proto.ResourceVersion != "12345" {
		t.Errorf("expected proto.ResourceVersion to be '12345', got '%s'", proto.ResourceVersion)
	}
	if proto.CreatedAt == nil || !proto.CreatedAt.AsTime().Equal(now) {
		t.Errorf("expected proto.CreatedAt to be %v, got %v", now, proto.CreatedAt)
	}

	crd, err := ProtoPgToCRD(proto)
	if err != nil {
		t.Fatalf("ProtoPgToCRD failed: %v", err)
	}

	if crd.Name != "test-pg" {
		t.Errorf("expected crd.Name to be 'test-pg', got '%s'", crd.Name)
	}
	if crd.Namespace != "test-ns" {
		t.Errorf("expected crd.Namespace to be 'test-ns', got '%s'", crd.Namespace)
	}
	if crd.ResourceVersion != "12345" {
		t.Errorf("expected crd.ResourceVersion to be '12345', got '%s'", crd.ResourceVersion)
	}
	if crd.Labels["env"] != "prod" {
		t.Errorf("expected crd.Labels['env'] to be 'prod', got '%s'", crd.Labels["env"])
	}
	if crd.Annotations["foo"] != "bar" {
		t.Errorf("expected crd.Annotations['foo'] to be 'bar', got '%s'", crd.Annotations["foo"])
	}
	if !crd.CreationTimestamp.Time.Equal(now) {
		t.Errorf("expected crd.CreationTimestamp to be %v, got %v", now, crd.CreationTimestamp)
	}
}

func TestPreviewGroup_RoundTrip_Proto_To_CRD_To_Proto(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := &pb.PreviewGroup{
		Name:            "test-pg",
		Namespace:       "test-ns",
		ResourceVersion: "12345",
		CreatedAt:       timestamppb.New(now),
		Labels:          map[string]string{"env": "prod"},
		Annotations:     map[string]string{"foo": "bar"},
		Spec:            &pb.PreviewGroupSpec{
			// Add dummy fields if necessary, currently tests without
		},
	}

	crd, err := ProtoPgToCRD(original)
	if err != nil {
		t.Fatalf("ProtoPgToCRD failed: %v", err)
	}
	if crd.Name != "test-pg" {
		t.Errorf("expected crd.Name to be 'test-pg', got '%s'", crd.Name)
	}
	if !crd.CreationTimestamp.Time.Equal(now) {
		t.Errorf("expected crd.CreationTimestamp to be %v, got %v", now, crd.CreationTimestamp)
	}

	proto, err := CRDPgToProto(crd)
	if err != nil {
		t.Fatalf("CRDPgToProto failed: %v", err)
	}

	if proto.Name != "test-pg" {
		t.Errorf("expected proto.Name to be 'test-pg', got '%s'", proto.Name)
	}
	if proto.ResourceVersion != "12345" {
		t.Errorf("expected proto.ResourceVersion to be '12345', got '%s'", proto.ResourceVersion)
	}
	if proto.Labels["env"] != "prod" {
		t.Errorf("expected proto.Labels['env'] to be 'prod', got '%s'", proto.Labels["env"])
	}
	if proto.Annotations["foo"] != "bar" {
		t.Errorf("expected proto.Annotations['foo'] to be 'bar', got '%s'", proto.Annotations["foo"])
	}
	if proto.CreatedAt == nil || !proto.CreatedAt.AsTime().Equal(now) {
		t.Errorf("expected proto.CreatedAt to be %v, got %v", now, proto.CreatedAt)
	}
}
