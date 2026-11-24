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

func TestCredentialRevision(t *testing.T) {
	if got := CredentialRevision(nil); got != "" {
		t.Fatalf("expected empty revision for empty map, got %q", got)
	}
	a := CredentialRevision(map[string]string{
		"DATABASE_URL": "postgres://secret-user:secret-pass@db:5432/app",
		"DB_HOST":      "db",
	})
	b := CredentialRevision(map[string]string{
		"DB_HOST":      "db",
		"DATABASE_URL": "postgres://secret-user:secret-pass@db:5432/app",
	})
	c := CredentialRevision(map[string]string{
		"DATABASE_URL": "postgres://new-user:new-pass@db:5432/app",
		"DB_HOST":      "db",
	})
	if len(a) != 16 {
		t.Fatalf("expected 16-hex revision, got %q", a)
	}
	if a != b {
		t.Fatalf("expected deterministic revision across key order, got %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("expected different revision for different DSN")
	}
	if strings.Contains(a, "secret-pass") || strings.Contains(a, "postgres") {
		t.Fatalf("revision must not leak secret contents: %q", a)
	}
}
