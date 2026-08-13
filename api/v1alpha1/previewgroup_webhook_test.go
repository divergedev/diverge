package v1alpha1

import (
	"testing"
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
