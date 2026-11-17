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

func TestPreviewGroup_ChangedServices_RoundTrip(t *testing.T) {
	crd := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pg",
			Namespace: "default",
		},
		Status: v1alpha1.PreviewGroupStatus{
			Phase: v1alpha1.PreviewGroupPhaseRunning,
			Services: []v1alpha1.PreviewGroupServiceStatus{
				{
					Name:            "orders-svc",
					Phase:           v1alpha1.PhaseRunning,
					ChangedServices: []string{"orders-svc", "cart-svc"},
				},
				{
					Name:            "auth-svc",
					Phase:           v1alpha1.PhaseRunning,
					ChangedServices: []string{},
				},
			},
		},
	}

	proto, err := CRDPgToProto(crd)
	if err != nil {
		t.Fatalf("CRDPgToProto failed: %v", err)
	}

	if len(proto.Status.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(proto.Status.Services))
	}
	if len(proto.Status.Services[0].ChangedServices) != 2 || proto.Status.Services[0].ChangedServices[0] != "orders-svc" {
		t.Errorf("expected changedServices ['orders-svc', 'cart-svc'], got %v", proto.Status.Services[0].ChangedServices)
	}

	roundTripped, err := ProtoPgToCRD(proto)
	if err != nil {
		t.Fatalf("ProtoPgToCRD failed: %v", err)
	}

	if len(roundTripped.Status.Services) != 2 {
		t.Fatalf("expected 2 roundtripped services, got %d", len(roundTripped.Status.Services))
	}
	if len(roundTripped.Status.Services[0].ChangedServices) != 2 || roundTripped.Status.Services[0].ChangedServices[0] != "orders-svc" {
		t.Errorf("expected roundtripped changedServices ['orders-svc', 'cart-svc'], got %v", roundTripped.Status.Services[0].ChangedServices)
	}
}

func TestEnvironment_StatusAsyncEnvVarsAndDatabaseProvider_RoundTrip(t *testing.T) {
	original := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-isolation",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Database: v1alpha1.EnvironmentDatabase{
				Mode: "schema",
			},
			Routing: v1alpha1.EnvironmentRouting{
				AsyncRoutes: []v1alpha1.AsyncRouteSpec{
					{
						Protocol: "redis",
						Target:   "cache",
					},
					{
						Protocol: "s3",
						Target:   "uploads",
					},
				},
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			Phase:            v1alpha1.PhaseRunning,
			DatabaseProvider: "schema",
			AsyncEnvVars: map[string]string{
				"DIVERGE_REDIS_PREFIX":   "diverge:env-isolation:",
				"DIVERGE_STORAGE_PREFIX": "previews/env-isolation/",
			},
		},
	}

	proto, err := CRDEnvToProto(original)
	if err != nil {
		t.Fatalf("CRDEnvToProto failed: %v", err)
	}

	if proto.Status == nil {
		t.Fatal("expected non-nil proto.Status")
	}
	if proto.Status.DatabaseProvider != "schema" {
		t.Errorf("expected proto.Status.DatabaseProvider to be 'schema', got %q", proto.Status.DatabaseProvider)
	}
	if proto.Status.AsyncEnvVars["DIVERGE_REDIS_PREFIX"] != "diverge:env-isolation:" {
		t.Errorf("expected DIVERGE_REDIS_PREFIX in proto.Status.AsyncEnvVars, got %v", proto.Status.AsyncEnvVars)
	}
	if proto.Status.AsyncEnvVars["DIVERGE_STORAGE_PREFIX"] != "previews/env-isolation/" {
		t.Errorf("expected DIVERGE_STORAGE_PREFIX in proto.Status.AsyncEnvVars, got %v", proto.Status.AsyncEnvVars)
	}

	roundTripped, err := ProtoEnvToCRD(proto)
	if err != nil {
		t.Fatalf("ProtoEnvToCRD failed: %v", err)
	}
	if roundTripped.Status.DatabaseProvider != "schema" {
		t.Errorf("expected roundTripped.Status.DatabaseProvider to be 'schema', got %q", roundTripped.Status.DatabaseProvider)
	}
	if roundTripped.Status.AsyncEnvVars["DIVERGE_REDIS_PREFIX"] != "diverge:env-isolation:" {
		t.Errorf("expected DIVERGE_REDIS_PREFIX in roundTripped.Status.AsyncEnvVars, got %v", roundTripped.Status.AsyncEnvVars)
	}
	if roundTripped.Status.AsyncEnvVars["DIVERGE_STORAGE_PREFIX"] != "previews/env-isolation/" {
		t.Errorf("expected DIVERGE_STORAGE_PREFIX in roundTripped.Status.AsyncEnvVars, got %v", roundTripped.Status.AsyncEnvVars)
	}
}

func TestMultiCluster_ClusterRefAndTargetCluster_RoundTrip(t *testing.T) {
	envCRD := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-federated",
			Namespace: "payments",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Mode: "delta",
				ClusterRef: &v1alpha1.ClusterTargetRef{
					Name: "us-west-burst",
					KubeconfigSecretRef: &v1alpha1.SecretRef{
						Name: "spoke-west-secret",
						Key:  "kubeconfig",
					},
				},
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			Phase:         v1alpha1.PhaseRunning,
			TargetCluster: "us-west-burst",
		},
	}

	envProto, err := CRDEnvToProto(envCRD)
	if err != nil {
		t.Fatalf("CRDEnvToProto failed: %v", err)
	}
	if envProto.GetSpec().GetDeploy().GetClusterRef().GetName() != "us-west-burst" {
		t.Fatalf("expected clusterRef.name us-west-burst, got %q", envProto.GetSpec().GetDeploy().GetClusterRef().GetName())
	}
	if envProto.GetSpec().GetDeploy().GetClusterRef().GetKubeconfigSecretRef().GetName() != "spoke-west-secret" {
		t.Fatalf("expected kubeconfigSecretRef.name spoke-west-secret, got %q", envProto.GetSpec().GetDeploy().GetClusterRef().GetKubeconfigSecretRef().GetName())
	}
	if envProto.GetStatus().GetTargetCluster() != "us-west-burst" {
		t.Fatalf("expected status.targetCluster us-west-burst, got %q", envProto.GetStatus().GetTargetCluster())
	}

	envBack, err := ProtoEnvToCRD(envProto)
	if err != nil {
		t.Fatalf("ProtoEnvToCRD failed: %v", err)
	}
	if envBack.Spec.Deploy.ClusterRef == nil || envBack.Spec.Deploy.ClusterRef.Name != "us-west-burst" {
		t.Fatalf("expected round-tripped ClusterRef us-west-burst, got %+v", envBack.Spec.Deploy.ClusterRef)
	}
	if envBack.Status.TargetCluster != "us-west-burst" {
		t.Fatalf("expected round-tripped TargetCluster us-west-burst, got %q", envBack.Status.TargetCluster)
	}

	pgCRD := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-fed"},
		Spec: v1alpha1.PreviewGroupSpec{
			ClusterRef: &v1alpha1.ClusterTargetRef{
				Name: "eu-central-1",
				KubeconfigSecretRef: &v1alpha1.SecretRef{
					Name: "eu-spoke-secret",
				},
			},
			Services: []v1alpha1.PreviewGroupServiceSpec{
				{
					Name: "checkout",
					ClusterRef: &v1alpha1.ClusterTargetRef{
						Name: "us-east-gpu",
						KubeconfigSecretRef: &v1alpha1.SecretRef{
							Name: "gpu-spoke-secret",
						},
					},
				},
			},
		},
		Status: v1alpha1.PreviewGroupStatus{
			Services: []v1alpha1.PreviewGroupServiceStatus{
				{Name: "checkout", Phase: v1alpha1.PhaseRunning, TargetCluster: "us-east-gpu"},
			},
		},
	}

	pgProto, err := CRDPgToProto(pgCRD)
	if err != nil {
		t.Fatalf("CRDPgToProto failed: %v", err)
	}
	if pgProto.GetSpec().GetClusterRef().GetName() != "eu-central-1" {
		t.Fatalf("expected group clusterRef eu-central-1, got %q", pgProto.GetSpec().GetClusterRef().GetName())
	}
	if pgProto.GetSpec().GetServices()[0].GetClusterRef().GetName() != "us-east-gpu" {
		t.Fatalf("expected service clusterRef us-east-gpu, got %q", pgProto.GetSpec().GetServices()[0].GetClusterRef().GetName())
	}
	if pgProto.GetStatus().GetServices()[0].GetTargetCluster() != "us-east-gpu" {
		t.Fatalf("expected service status targetCluster us-east-gpu, got %q", pgProto.GetStatus().GetServices()[0].GetTargetCluster())
	}

	pgBack, err := ProtoPgToCRD(pgProto)
	if err != nil {
		t.Fatalf("ProtoPgToCRD failed: %v", err)
	}
	if pgBack.Spec.ClusterRef == nil || pgBack.Spec.ClusterRef.Name != "eu-central-1" {
		t.Fatalf("expected round-tripped group ClusterRef eu-central-1, got %+v", pgBack.Spec.ClusterRef)
	}
	if pgBack.Status.Services[0].TargetCluster != "us-east-gpu" {
		t.Fatalf("expected round-tripped service TargetCluster us-east-gpu, got %q", pgBack.Status.Services[0].TargetCluster)
	}
}
