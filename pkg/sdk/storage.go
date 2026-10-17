package sdk

import (
	"context"
	"os"
	"strings"
)

var storagePrefixEnvVars = []string{
	"DIVERGE_STORAGE_PREFIX",
	"S3_KEY_PREFIX",
	"AWS_S3_PREFIX",
	"GCS_KEY_PREFIX",
}

// ResolveStoragePrefix returns the active S3/GCS/MinIO object key prefix for the current preview environment.
// Priority:
//  1. DIVERGE_STORAGE_PREFIX, S3_KEY_PREFIX, AWS_S3_PREFIX, or GCS_KEY_PREFIX environment variable
//     (trimmed of leading "/" and normalized with a trailing "/").
//  2. Active environment from context (or DIVERGE_ENV fallback), formatted as "previews/<env>/".
//  3. Empty string "" when running in baseline/production without an active preview environment.
func ResolveStoragePrefix(ctx context.Context) string {
	for _, envKey := range storagePrefixEnvVars {
		if raw := strings.TrimSpace(os.Getenv(envKey)); raw != "" {
			trimmed := strings.Trim(raw, "/")
			if trimmed != "" {
				return trimmed + "/"
			}
		}
	}

	var env string
	if ctx != nil {
		env = strings.Trim(strings.TrimSpace(EnvironmentFromContext(ctx)), "/")
	}
	if env == "" {
		env = strings.Trim(strings.TrimSpace(os.Getenv("DIVERGE_ENV")), "/")
	}
	if env != "" {
		return "previews/" + env + "/"
	}
	return ""
}

// ResolveStorageKey prefixes an S3/GCS/MinIO object key with the active preview prefix.
// It is idempotent: if key already starts with the resolved prefix, it is returned without duplicate prefixing.
func ResolveStorageKey(ctx context.Context, key string) string {
	prefix := ResolveStoragePrefix(ctx)
	if prefix == "" {
		return key
	}
	cleanKey := strings.TrimLeft(key, "/")
	if strings.HasPrefix(key, prefix) {
		return key
	}
	if strings.HasPrefix(cleanKey, prefix) {
		return cleanKey
	}
	return prefix + cleanKey
}
