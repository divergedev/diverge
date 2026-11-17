package v1alpha1

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidatePreviewGroup(t *testing.T) {
	tests := []struct {
		name      string
		pg        *PreviewGroup
		wantErrs  int
		wantField string // substring expected in at least one error
	}{
		{
			name: "valid minimal image mode",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source: EnvironmentSource{
						Provider: "gitlab",
						Project:  "azra/platform",
						Branch:   "feat/payments",
					},
					Routing: PreviewGroupRouting{
						HeaderValue: "42",
					},
					Services: []PreviewGroupServiceSpec{
						{Name: "payments-api", Image: "registry.azra-ai.com/payments:mr-42"},
					},
				},
			},
			wantErrs: 0,
		},
		{
			name: "valid with all modes",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source: EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{
						HeaderValue: "feature-xyz",
					},
					Services: []PreviewGroupServiceSpec{
						{Name: "payments-api", Image: "registry/payments:v1", Mode: ServiceModeImage},
						{Name: "consent-mgr", Mode: ServiceModeBaseline},
						{Name: "auth-svc", Mode: ServiceModeLocal, Endpoint: "100.64.23.42:8080"},
					},
				},
			},
			wantErrs: 0,
		},
		{
			name: "valid with grpc protocol and resources",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{HeaderValue: "99"},
					Services: []PreviewGroupServiceSpec{
						{
							Name:     "grpc-svc",
							Image:    "registry/grpc:v1",
							Protocol: ServiceProtocolGRPC,
							Resources: &ResourceOverride{
								CPURequest:    "100m",
								MemoryRequest: "256Mi",
							},
						},
					},
				},
			},
			wantErrs: 0,
		},
		{
			name: "missing headerValue",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{},
					Services: []PreviewGroupServiceSpec{
						{Name: "svc", Image: "img:v1"},
					},
				},
			},
			wantErrs:  1,
			wantField: "headerValue",
		},
		{
			name: "no services",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:   EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing:  PreviewGroupRouting{HeaderValue: "42"},
					Services: []PreviewGroupServiceSpec{},
				},
			},
			wantErrs:  1,
			wantField: "services",
		},
		{
			name: "image mode missing image",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{HeaderValue: "42"},
					Services: []PreviewGroupServiceSpec{
						{Name: "svc", Mode: ServiceModeImage},
					},
				},
			},
			wantErrs:  1,
			wantField: "image",
		},
		{
			name: "image mode with endpoint is invalid",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{HeaderValue: "42"},
					Services: []PreviewGroupServiceSpec{
						{Name: "svc", Image: "img:v1", Mode: ServiceModeImage, Endpoint: "1.2.3.4:8080"},
					},
				},
			},
			wantErrs:  1,
			wantField: "endpoint",
		},
		{
			name: "local mode missing endpoint",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{HeaderValue: "42"},
					Services: []PreviewGroupServiceSpec{
						{Name: "svc", Mode: ServiceModeLocal},
					},
				},
			},
			wantErrs:  1,
			wantField: "endpoint",
		},
		{
			name: "local mode with image is invalid",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{HeaderValue: "42"},
					Services: []PreviewGroupServiceSpec{
						{Name: "svc", Mode: ServiceModeLocal, Endpoint: "1.2.3.4:8080", Image: "img:v1"},
					},
				},
			},
			wantErrs:  1,
			wantField: "image",
		},
		{
			name: "local mode bad endpoint format",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{HeaderValue: "42"},
					Services: []PreviewGroupServiceSpec{
						{Name: "svc", Mode: ServiceModeLocal, Endpoint: "not-valid"},
					},
				},
			},
			wantErrs:  1,
			wantField: "endpoint",
		},
		{
			name: "local mode endpoint missing port",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{HeaderValue: "42"},
					Services: []PreviewGroupServiceSpec{
						{Name: "svc", Mode: ServiceModeLocal, Endpoint: "100.64.1.1"},
					},
				},
			},
			wantErrs:  1,
			wantField: "endpoint",
		},
		{
			name: "local mode endpoint with non-numeric port",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{HeaderValue: "42"},
					Services: []PreviewGroupServiceSpec{
						{Name: "svc", Mode: ServiceModeLocal, Endpoint: "100.64.1.1:abc"},
					},
				},
			},
			wantErrs:  1,
			wantField: "endpoint",
		},
		{
			name: "local mode endpoint with port 0",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{HeaderValue: "42"},
					Services: []PreviewGroupServiceSpec{
						{Name: "svc", Mode: ServiceModeLocal, Endpoint: "100.64.1.1:0"},
					},
				},
			},
			wantErrs:  1,
			wantField: "endpoint",
		},
		{
			name: "local mode endpoint with port 65536",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{HeaderValue: "42"},
					Services: []PreviewGroupServiceSpec{
						{Name: "svc", Mode: ServiceModeLocal, Endpoint: "100.64.1.1:65536"},
					},
				},
			},
			wantErrs:  1,
			wantField: "endpoint",
		},
		{
			name: "baseline mode with image is invalid",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{HeaderValue: "42"},
					Services: []PreviewGroupServiceSpec{
						{Name: "svc", Mode: ServiceModeBaseline, Image: "img:v1"},
					},
				},
			},
			wantErrs:  1,
			wantField: "image",
		},
		{
			name: "blocked namespace",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{HeaderValue: "42"},
					Services: []PreviewGroupServiceSpec{
						{Name: "svc", Image: "img:v1", Namespace: "kube-system"},
					},
				},
			},
			wantErrs:  1,
			wantField: "namespace",
		},
		{
			name: "duplicate service names",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{HeaderValue: "42"},
					Services: []PreviewGroupServiceSpec{
						{Name: "payments-api", Image: "img:v1"},
						{Name: "payments-api", Image: "img:v2"},
					},
				},
			},
			wantErrs:  1,
			wantField: "name",
		},
		{
			name: "pathPrefix without leading slash",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{HeaderValue: "42"},
					Services: []PreviewGroupServiceSpec{
						{Name: "svc", Image: "img:v1", PathPrefix: "api/payments"},
					},
				},
			},
			wantErrs:  1,
			wantField: "pathPrefix",
		},
		{
			name: "multiple errors compound",
			pg: &PreviewGroup{
				Spec: PreviewGroupSpec{
					Source:  EnvironmentSource{Provider: "gitlab", Project: "azra/platform", Branch: "main"},
					Routing: PreviewGroupRouting{}, // missing headerValue
					Services: []PreviewGroupServiceSpec{
						{Name: "", Mode: ServiceModeLocal}, // missing name, missing endpoint
					},
				},
			},
			wantErrs: 3, // headerValue + name + endpoint
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validatePreviewGroup(tt.pg)
			if len(errs) != tt.wantErrs {
				t.Errorf("got %d errors, want %d:\n%v", len(errs), tt.wantErrs, errs)
				return
			}
			if tt.wantField != "" && len(errs) > 0 {
				found := false
				for _, e := range errs {
					if contains(e.Field, tt.wantField) || contains(e.Detail, tt.wantField) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error mentioning %q, got: %v", tt.wantField, errs)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsHelper(s, substr)
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestPreviewGroupValidateUpdate_ClusterRefImmutability(t *testing.T) {
	v := &previewGroupValidator{}
	ctx := context.Background()

	oldPG := &PreviewGroup{
		Spec: PreviewGroupSpec{
			Source:  EnvironmentSource{Provider: "github", Project: "org/repo", Branch: "feat/pg"},
			Routing: PreviewGroupRouting{HeaderValue: "pr-99"},
			ClusterRef: &ClusterTargetRef{
				Name:                "spoke-west",
				KubeconfigSecretRef: &SecretRef{Name: "west-secret"},
			},
			Services: []PreviewGroupServiceSpec{
				{Name: "api", Image: "api:v1"},
				{Name: "billing", Mode: ServiceModeBaseline},
			},
		},
	}

	// 1. Changing group-level clusterRef mutates existing service "api"'s effective target -> forbidden.
	groupChanged := oldPG.DeepCopy()
	groupChanged.Spec.ClusterRef = &ClusterTargetRef{
		Name:                "spoke-east",
		KubeconfigSecretRef: &SecretRef{Name: "east-secret"},
	}
	if _, err := v.ValidateUpdate(ctx, oldPG, groupChanged); err == nil {
		t.Fatal("expected group-level clusterRef change on existing child service to be rejected")
	}

	// 2. Overriding service-level clusterRef on existing service "api" -> forbidden.
	svcChanged := oldPG.DeepCopy()
	svcChanged.Spec.Services[0].ClusterRef = &ClusterTargetRef{
		Name:                "spoke-east",
		KubeconfigSecretRef: &SecretRef{Name: "east-secret"},
	}
	if _, err := v.ValidateUpdate(ctx, oldPG, svcChanged); err == nil {
		t.Fatal("expected service-level clusterRef change on existing child service to be rejected")
	}

	// 3. Updating image on existing service and adding a brand-new service on another spoke -> allowed.
	validUpdate := oldPG.DeepCopy()
	validUpdate.Spec.Services[0].Image = "api:v2"
	validUpdate.Spec.Services = append(validUpdate.Spec.Services, PreviewGroupServiceSpec{
		Name:  "worker",
		Image: "worker:v1",
		ClusterRef: &ClusterTargetRef{
			Name:                "spoke-east",
			KubeconfigSecretRef: &SecretRef{Name: "east-secret"},
		},
	})
	if _, err := v.ValidateUpdate(ctx, oldPG, validUpdate); err != nil {
		t.Fatalf("expected image update + new service addition to succeed, got: %v", err)
	}

	// 4. Deleting PreviewGroup still blocks effective clusterRef mutation while skipping structural checks.
	delNow := metav1.Now()
	deletingChanged := groupChanged.DeepCopy()
	deletingChanged.DeletionTimestamp = &delNow
	if _, err := v.ValidateUpdate(ctx, oldPG, deletingChanged); err == nil {
		t.Fatal("expected deleting PreviewGroup to reject clusterRef mutation")
	}
	deletingUnchanged := oldPG.DeepCopy()
	deletingUnchanged.DeletionTimestamp = &delNow
	deletingUnchanged.Spec.Routing.HeaderValue = ""
	if _, err := v.ValidateUpdate(ctx, oldPG, deletingUnchanged); err != nil {
		t.Fatalf("expected deleting PreviewGroup with unchanged clusterRef to succeed, got: %v", err)
	}

	// 5. Remote clusterRef resolving to reserved "local" sentinel is rejected on both group and service specs.
	localSentinelGroup := oldPG.DeepCopy()
	localSentinelGroup.Spec.ClusterRef = &ClusterTargetRef{
		KubeconfigSecretRef: &SecretRef{Name: "local"},
	}
	if _, err := v.ValidateCreate(ctx, localSentinelGroup); err == nil {
		t.Fatal("expected group-level remote clusterRef resolving to \"local\" to be rejected")
	}
	localSentinelSvc := oldPG.DeepCopy()
	localSentinelSvc.Spec.Services[0].ClusterRef = &ClusterTargetRef{
		Name:                "local",
		KubeconfigSecretRef: &SecretRef{Name: "spoke-sec"},
	}
	if _, err := v.ValidateCreate(ctx, localSentinelSvc); err == nil {
		t.Fatal("expected service-level remote clusterRef named \"local\" to be rejected")
	}
}
