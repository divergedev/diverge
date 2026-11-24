package database

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
)

const (
	// DBEnvSecretPrefix is the Kubernetes Secret name prefix for per-service generated DB credentials.
	DBEnvSecretPrefix = "diverge-db-env-"
	// GroupDBEnvSecretPrefix is the Kubernetes Secret name prefix for PreviewGroup shared DB credentials.
	GroupDBEnvSecretPrefix = "diverge-group-db-env-"
)

var (
	secretNameNonAlnum  = regexp.MustCompile(`[^a-z0-9-]`)
	secretNameMultiDash = regexp.MustCompile(`-{2,}`)
)

// SanitizeK8sSecretName produces a deterministic, DNS-1123 compliant Kubernetes resource name
// bounded by maxLength with an 8-character SHA-256 suffix derived from hashInput.
func SanitizeK8sSecretName(raw, hashInput string, maxLength int) string {
	if maxLength < 10 {
		maxLength = 10
	}
	raw = strings.ToLower(raw)
	raw = strings.NewReplacer(".", "-", "_", "-").Replace(raw)
	raw = secretNameNonAlnum.ReplaceAllString(raw, "")
	raw = secretNameMultiDash.ReplaceAllString(raw, "-")
	raw = strings.Trim(raw, "-")

	if raw == "" {
		raw = "resource"
	}

	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))[:8]
	prefix := raw
	if len(prefix) > maxLength-9 {
		prefix = strings.TrimRight(prefix[:maxLength-9], "-")
	}
	if prefix == "" {
		prefix = "r"
	}
	return prefix + "-" + hash
}

// EnvSecretName returns the deterministic Secret name for a service's database credentials.
func EnvSecretName(envName string) string {
	return SanitizeK8sSecretName(DBEnvSecretPrefix+envName, envName, 63)
}

// GroupEnvSecretName returns the deterministic Secret name for a PreviewGroup child's shared DB credentials.
func GroupEnvSecretName(childEnvName string) string {
	return SanitizeK8sSecretName(GroupDBEnvSecretPrefix+childEnvName, childEnvName, 63)
}
