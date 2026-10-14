package v1alpha1

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidateEnvironment(t *testing.T) {
	tests := []struct {
		name      string
		env       *Environment
		wantErrs  int
		wantField string
	}{
		{
			name: "valid minimal environment",
			env: &Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pr-42",
					Namespace: "default",
				},
				Spec: EnvironmentSpec{
					Source: EnvironmentSource{
						Provider: "github",
						Project:  "org/repo",
						Branch:   "feat/test",
					},
				},
			},
			wantErrs: 0,
		},
		{
			name: "valid environment with same-namespace SecretRefs and subdomain",
			env: &Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pr-42",
					Namespace: "preview",
				},
				Spec: EnvironmentSpec{
					Source: EnvironmentSource{
						Provider: "gitlab",
						Branch:   "main",
					},
					Routing: EnvironmentRouting{
						Mode:       "subdomain",
						BaseDomain: "preview.example.com",
					},
					Deploy: EnvironmentDeploy{
						Manifests: &ManifestSource{
							Type: "url",
							URL:  "https://example.com/manifests.yaml",
						},
						EnvFrom: []SecretRef{
							{Name: "app-secret", Namespace: "preview"},
							{Name: "default-ns-secret"},
						},
					},
					Database: EnvironmentDatabase{
						Atlas: &AtlasSpec{
							Mode:               "versioned",
							MigrationConfigMap: "migrations-cm",
						},
						MigrationJob: &MigrationJobSpec{
							Image: "migrate/migrate:v4",
							EnvFrom: []SecretRef{
								{Name: "db-secret", Namespace: "preview"},
							},
						},
					},
					ServiceConfig: &ServicePreviewConfig{
						ServiceName: "api",
						Port:        8080,
						Image:       "api:v1",
						Endpoint:    "100.64.0.1:8080",
					},
					EnvFrom: []SecretRef{
						{Name: "env-secret", Namespace: "preview"},
					},
				},
			},
			wantErrs: 0,
		},
		{
			name: "blocked namespace kube-system",
			env: &Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pr-42",
					Namespace: "kube-system",
				},
				Spec: EnvironmentSpec{
					Source: EnvironmentSource{
						Provider: "github",
						Branch:   "main",
					},
				},
			},
			wantErrs:  1,
			wantField: "metadata.namespace",
		},
		{
			name: "missing source provider and branch",
			env: &Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pr-42",
					Namespace: "default",
				},
				Spec: EnvironmentSpec{},
			},
			wantErrs:  2,
			wantField: "spec.source.provider",
		},
		{
			name: "unsupported source provider",
			env: &Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pr-42",
					Namespace: "default",
				},
				Spec: EnvironmentSpec{
					Source: EnvironmentSource{
						Provider: "bitbucket",
						Branch:   "main",
					},
				},
			},
			wantErrs:  1,
			wantField: "spec.source.provider",
		},
		{
			name: "cross-namespace SecretRef in spec.envFrom",
			env: &Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pr-42",
					Namespace: "team-a",
				},
				Spec: EnvironmentSpec{
					Source: EnvironmentSource{
						Provider: "github",
						Branch:   "main",
					},
					EnvFrom: []SecretRef{
						{Name: "other-secret", Namespace: "team-b"},
					},
				},
			},
			wantErrs:  1,
			wantField: "spec.envFrom[0].namespace",
		},
		{
			name: "cross-namespace SecretRef in spec.deploy.envFrom and migrationJob.envFrom",
			env: &Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pr-42",
					Namespace: "team-a",
				},
				Spec: EnvironmentSpec{
					Source: EnvironmentSource{
						Provider: "github",
						Branch:   "main",
					},
					Deploy: EnvironmentDeploy{
						EnvFrom: []SecretRef{
							{Name: "deploy-secret", Namespace: "kube-system"},
						},
					},
					Database: EnvironmentDatabase{
						MigrationJob: &MigrationJobSpec{
							EnvFrom: []SecretRef{
								{Name: "db-secret", Namespace: "prod"},
							},
						},
					},
				},
			},
			wantErrs:  2,
			wantField: "cross-namespace SecretRef is not allowed",
		},
		{
			name: "subdomain routing without baseDomain",
			env: &Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pr-42",
					Namespace: "default",
				},
				Spec: EnvironmentSpec{
					Source: EnvironmentSource{
						Provider: "github",
						Branch:   "main",
					},
					Routing: EnvironmentRouting{
						Mode: "subdomain",
					},
				},
			},
			wantErrs:  1,
			wantField: "spec.routing.baseDomain",
		},
		{
			name: "atlas versioned without migrationConfigMap",
			env: &Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pr-42",
					Namespace: "default",
				},
				Spec: EnvironmentSpec{
					Source: EnvironmentSource{
						Provider: "github",
						Branch:   "main",
					},
					Database: EnvironmentDatabase{
						Atlas: &AtlasSpec{
							Mode: "versioned",
						},
					},
				},
			},
			wantErrs:  1,
			wantField: "spec.database.atlas.migrationConfigMap",
		},
		{
			name: "atlas declarative without schemaConfigMap",
			env: &Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pr-42",
					Namespace: "default",
				},
				Spec: EnvironmentSpec{
					Source: EnvironmentSource{
						Provider: "github",
						Branch:   "main",
					},
					Database: EnvironmentDatabase{
						Atlas: &AtlasSpec{
							Mode: "declarative",
						},
					},
				},
			},
			wantErrs:  1,
			wantField: "spec.database.atlas.schemaConfigMap",
		},
		{
			name: "manifests url without url value",
			env: &Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pr-42",
					Namespace: "default",
				},
				Spec: EnvironmentSpec{
					Source: EnvironmentSource{
						Provider: "github",
						Branch:   "main",
					},
					Deploy: EnvironmentDeploy{
						Manifests: &ManifestSource{
							Type: "url",
						},
					},
				},
			},
			wantErrs:  1,
			wantField: "spec.deploy.manifests.url",
		},
		{
			name: "manifests url with invalid scheme",
			env: &Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pr-42",
					Namespace: "default",
				},
				Spec: EnvironmentSpec{
					Source: EnvironmentSource{
						Provider: "github",
						Branch:   "main",
					},
					Deploy: EnvironmentDeploy{
						Manifests: &ManifestSource{
							Type: "url",
							URL:  "ftp://example.com/manifests.yaml",
						},
					},
				},
			},
			wantErrs:  1,
			wantField: "spec.deploy.manifests.url",
		},
		{
			name: "invalid serviceConfig endpoint",
			env: &Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pr-42",
					Namespace: "default",
				},
				Spec: EnvironmentSpec{
					Source: EnvironmentSource{
						Provider: "local",
						Branch:   "dev",
					},
					ServiceConfig: &ServicePreviewConfig{
						ServiceName: "api",
						Endpoint:    "invalid-endpoint",
					},
				},
			},
			wantErrs:  1,
			wantField: "spec.serviceConfig.endpoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateEnvironment(tt.env)
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

func TestEnvironmentValidatorLifecycle(t *testing.T) {
	v := &environmentValidator{}
	ctx := context.Background()

	validEnv := &Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-1",
			Namespace: "default",
		},
		Spec: EnvironmentSpec{
			Source: EnvironmentSource{
				Provider: "github",
				Branch:   "main",
			},
		},
	}

	if _, err := v.ValidateCreate(ctx, validEnv); err != nil {
		t.Errorf("ValidateCreate on valid env returned error: %v", err)
	}

	invalidEnv := &Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-1",
			Namespace: "kube-system",
		},
		Spec: EnvironmentSpec{
			Source: EnvironmentSource{
				Provider: "github",
				Branch:   "main",
			},
		},
	}

	if _, err := v.ValidateCreate(ctx, invalidEnv); err == nil {
		t.Error("ValidateCreate on invalid env expected error, got nil")
	}

	if _, err := v.ValidateUpdate(ctx, validEnv, invalidEnv); err == nil {
		t.Error("ValidateUpdate to invalid env expected error, got nil")
	}

	if _, err := v.ValidateUpdate(ctx, validEnv, validEnv); err != nil {
		t.Errorf("ValidateUpdate to valid env returned error: %v", err)
	}

	if _, err := v.ValidateDelete(ctx, invalidEnv); err != nil {
		t.Errorf("ValidateDelete returned error: %v", err)
	}
}
