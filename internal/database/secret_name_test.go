package database

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestSecretNameHelpers(t *testing.T) {
	shortEnv := "pg_api.pr-42"
	expectedHash := fmt.Sprintf("%x", sha256.Sum256([]byte(shortEnv)))[:8]

	envSec := EnvSecretName(shortEnv)
	if envSec != "diverge-db-env-pg-api-pr-42-"+expectedHash {
		t.Fatalf("unexpected EnvSecretName: %q", envSec)
	}
	if len(envSec) > 63 {
		t.Fatalf("EnvSecretName exceeded 63 chars: %d", len(envSec))
	}

	groupSec := GroupEnvSecretName(shortEnv)
	if groupSec != "diverge-group-db-env-pg-api-pr-42-"+expectedHash {
		t.Fatalf("unexpected GroupEnvSecretName: %q", groupSec)
	}
	if len(groupSec) > 63 {
		t.Fatalf("GroupEnvSecretName exceeded 63 chars: %d", len(groupSec))
	}

	longEnv := strings.Repeat("service-a.", 10) + "env"
	longHash := fmt.Sprintf("%x", sha256.Sum256([]byte(longEnv)))[:8]

	longEnvSec := EnvSecretName(longEnv)
	if len(longEnvSec) > 63 {
		t.Fatalf("long EnvSecretName exceeded 63 chars (%d): %q", len(longEnvSec), longEnvSec)
	}
	if !strings.HasPrefix(longEnvSec, DBEnvSecretPrefix) || !strings.HasSuffix(longEnvSec, "-"+longHash) {
		t.Fatalf("unexpected truncated EnvSecretName: %q", longEnvSec)
	}

	longGroupSec := GroupEnvSecretName(longEnv)
	if len(longGroupSec) > 63 {
		t.Fatalf("long GroupEnvSecretName exceeded 63 chars (%d): %q", len(longGroupSec), longGroupSec)
	}
	if !strings.HasPrefix(longGroupSec, GroupDBEnvSecretPrefix) || !strings.HasSuffix(longGroupSec, "-"+longHash) {
		t.Fatalf("unexpected truncated GroupEnvSecretName: %q", longGroupSec)
	}

	if EnvSecretName(longEnv) != longEnvSec || GroupEnvSecretName(longEnv) != longGroupSec {
		t.Fatalf("secret naming helpers must be deterministic")
	}
}
