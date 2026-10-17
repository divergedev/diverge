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

// FormatStorageKeyPrefix returns the deterministic object key prefix for an environment, route target, and optional namespace.
func FormatStorageKeyPrefix(target, envName string, namespace ...string) string {
	trimmedTarget := strings.Trim(strings.TrimSpace(target), "/")
	trimmedEnv := strings.Trim(strings.TrimSpace(envName), "/")
	var ns string
	if len(namespace) > 0 {
		ns = strings.Trim(strings.TrimSpace(namespace[0]), "/")
	}
	if ns != "" {
		if trimmedTarget == "" || trimmedTarget == "default" {
			return fmt.Sprintf("previews/%s/%s/", ns, trimmedEnv)
		}
		return fmt.Sprintf("previews/%s/%s/%s/", ns, trimmedEnv, trimmedTarget)
	}
	if trimmedTarget == "" || trimmedTarget == "default" {
		return fmt.Sprintf("previews/%s/", trimmedEnv)
	}
	return fmt.Sprintf("previews/%s/%s/", trimmedEnv, trimmedTarget)
}

func isStorageProtocol(protocol string) bool {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "s3", "gcs", "minio":
		return true
	default:
		return false
	}
}

func sanitizeStorageTargetEnvSuffix(target string) string {
	trimmed := strings.Trim(strings.TrimSpace(target), "/")
	if trimmed == "" || strings.EqualFold(trimmed, "default") {
		return "DEFAULT"
	}
	upper := strings.ToUpper(trimmed)
	var b strings.Builder
	lastUnderscore := false
	for i := 0; i < len(upper); i++ {
		ch := upper[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			b.WriteByte(ch)
			lastUnderscore = false
		} else if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	res := strings.Trim(b.String(), "_")
	if res == "" {
		return "DEFAULT"
	}
	return res
}

// Provision computes the preview object storage key prefix and injects environment variables.
func (s *StorageProvisioner) Provision(_ context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) (*ProvisionResult, error) {
	if env == nil || strings.TrimSpace(env.Name) == "" {
		return nil, fmt.Errorf("environment with non-empty name is required")
	}

	ns := strings.Trim(strings.TrimSpace(env.Namespace), "/")
	if ns == "" {
		ns = "default"
	}
	prefix := FormatStorageKeyPrefix(route.Target, env.Name, ns)
	bucket := s.resolveBucket(env, route)
	endpoint := s.resolveEndpoint(env)

	envVars := make(map[string]string)
	if len(route.EnvVarMapping) == 0 {
		trimmedTarget := strings.Trim(strings.TrimSpace(route.Target), "/")
		suffix := sanitizeStorageTargetEnvSuffix(route.Target)

		defaultStorageRouteCount := 0
		sameSuffixCount := 0
		for _, r := range env.Spec.Routing.AsyncRoutes {
			if isStorageProtocol(r.Protocol) && len(r.EnvVarMapping) == 0 {
				defaultStorageRouteCount++
				if sanitizeStorageTargetEnvSuffix(r.Target) == suffix {
					sameSuffixCount++
				}
			}
		}
		if defaultStorageRouteCount > 1 && sameSuffixCount > 1 {
			return nil, fmt.Errorf("multiple storage async routes with target %q require explicit envVarMapping", route.Target)
		}

		isNamedTarget := trimmedTarget != "" && !strings.EqualFold(trimmedTarget, "default")
		if defaultStorageRouteCount <= 1 || !isNamedTarget {
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
		}
		if isNamedTarget {
			envVars["DIVERGE_STORAGE_PREFIX_"+suffix] = prefix
			envVars["S3_KEY_PREFIX_"+suffix] = prefix
			envVars["AWS_S3_PREFIX_"+suffix] = prefix
			envVars["GCS_KEY_PREFIX_"+suffix] = prefix
			if bucket != "" {
				envVars["DIVERGE_STORAGE_BUCKET_"+suffix] = bucket
			}
			if endpoint != "" {
				envVars["DIVERGE_STORAGE_ENDPOINT_"+suffix] = endpoint
			}
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
