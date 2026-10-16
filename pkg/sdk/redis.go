package sdk

import (
	"context"
	"os"
	"strings"
)

// ResolveRedisPrefix returns the active Redis/Valkey key prefix for the current preview environment.
// Priority:
//  1. DIVERGE_REDIS_PREFIX or REDIS_KEY_PREFIX environment variable (ensuring a trailing ":").
//  2. Active environment from context (or DIVERGE_ENV fallback), formatted as "preview:<env>:".
//  3. Empty string "" when running in baseline/production without an active preview environment.
func ResolveRedisPrefix(ctx context.Context) string {
	prefix := strings.TrimSpace(os.Getenv("DIVERGE_REDIS_PREFIX"))
	if prefix == "" {
		prefix = strings.TrimSpace(os.Getenv("REDIS_KEY_PREFIX"))
	}
	if prefix != "" {
		if !strings.HasSuffix(prefix, ":") {
			prefix += ":"
		}
		return prefix
	}

	var env string
	if ctx != nil {
		env = strings.TrimSpace(EnvironmentFromContext(ctx))
	}
	if env == "" {
		env = strings.TrimSpace(os.Getenv("DIVERGE_ENV"))
	}
	if env != "" {
		return "preview:" + env + ":"
	}
	return ""
}

// ResolveRedisKey prefixes a Redis/Valkey key with the active preview prefix.
// It is idempotent: if key already starts with the resolved prefix, it is returned unchanged.
func ResolveRedisKey(ctx context.Context, key string) string {
	prefix := ResolveRedisPrefix(ctx)
	if prefix == "" || strings.HasPrefix(key, prefix) {
		return key
	}
	return prefix + key
}
