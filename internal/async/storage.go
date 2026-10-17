package async

import (
	"context"
	"fmt"
	"os"
	"strings"

	v1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

const (
	AnnotationStorageEndpoint = "divergedev.com/storage-endpoint"
	AnnotationStorageBucket   = "divergedev.com/storage-bucket"
)

// StorageProvisioner provisions object key-prefix isolation for S3, GCS, and MinIO
// preview environments so preview uploads/mutations do not collide with baseline buckets.
type StorageProvisioner struct {
	Endpoint string // Optional S3/MinIO/GCS endpoint override
	Bucket   string // Optional default bucket name
	Provider string // "s3", "gcs", or "minio" (default "s3")
}

// Name returns the provisioner name ("s3", "gcs", or "minio").
func (s *StorageProvisioner) Name() string {
	if p := strings.TrimSpace(s.Provider); p != "" {
		return p
	}
	return "s3"
}

func (s *StorageProvisioner) resolveEndpoint(env *v1alpha1.Environment) string {
	if env != nil && env.Annotations != nil {
		if ep := strings.TrimSpace(env.Annotations[AnnotationStorageEndpoint]); ep != "" {
			return ep
		}
	}
	if ep := strings.TrimSpace(s.Endpoint); ep != "" {
		return ep
	}
	return strings.TrimSpace(os.Getenv("DIVERGE_STORAGE_ENDPOINT"))
}

func (s *StorageProvisioner) resolveBucket(env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) string {
	if env != nil && env.Annotations != nil {
		if b := strings.TrimSpace(env.Annotations[AnnotationStorageBucket]); b != "" {
			return b
		}
	}
	if b := strings.TrimSpace(s.Bucket); b != "" {
		return b
	}
	if b := strings.TrimSpace(os.Getenv("DIVERGE_STORAGE_BUCKET")); b != "" {
		return b
	}
	trimmedTarget := strings.Trim(strings.TrimSpace(route.Target), "/")
	if trimmedTarget != "" && trimmedTarget != "default" {
		return trimmedTarget
	}
	return ""
}

// FormatStorageKeyPrefix returns the deterministic object key prefix for an environment and route target.
func FormatStorageKeyPrefix(target, envName string) string {
	trimmedTarget := strings.Trim(strings.TrimSpace(target), "/")
	trimmedEnv := strings.Trim(strings.TrimSpace(envName), "/")
	if trimmedTarget == "" || trimmedTarget == "default" {
		return fmt.Sprintf("previews/%s/", trimmedEnv)
	}
	return fmt.Sprintf("previews/%s/%s/", trimmedEnv, trimmedTarget)
}

// Provision computes the preview object storage key prefix and injects environment variables.
func (s *StorageProvisioner) Provision(_ context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) (*ProvisionResult, error) {
	if env == nil || strings.TrimSpace(env.Name) == "" {
		return nil, fmt.Errorf("environment with non-empty name is required")
	}

	prefix := FormatStorageKeyPrefix(route.Target, env.Name)
	bucket := s.resolveBucket(env, route)
	endpoint := s.resolveEndpoint(env)

	envVars := make(map[string]string)
	if len(route.EnvVarMapping) == 0 {
		envVars["DIVERGE_STORAGE_PREFIX"] = prefix
		envVars["S3_KEY_PREFIX"] = prefix
		envVars["AWS_S3_PREFIX"] = prefix
		envVars["GCS_KEY_PREFIX"] = prefix
		if bucket != "" {
			envVars["DIVERGE_STORAGE_BUCKET"] = bucket
		}
		if endpoint != "" {
			envVars["DIVERGE_STORAGE_ENDPOINT"] = endpoint
		}
	} else {
		for envVar, tmpl := range route.EnvVarMapping {
			if tmpl == "{{ .ResolvedTarget }}" || tmpl == "" {
				envVars[envVar] = prefix
			} else {
				envVars[envVar] = strings.ReplaceAll(tmpl, "{{ .ResolvedTarget }}", prefix)
			}
		}
	}

	return &ProvisionResult{
		ResolvedTarget: prefix,
		EnvVars:        envVars,
	}, nil
}

// Teardown validates the environment for storage key-prefix routes.
func (s *StorageProvisioner) Teardown(_ context.Context, env *v1alpha1.Environment, _ v1alpha1.AsyncRouteSpec) error {
	if env == nil || strings.TrimSpace(env.Name) == "" {
		return fmt.Errorf("environment with non-empty name is required")
	}
	return nil
}
