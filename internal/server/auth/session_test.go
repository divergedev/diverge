package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestSessionManager_MintAndVerify(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	sm, err := NewSessionManager(SessionConfig{
		SigningKey: key,
		MaxAge:     1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	token, err := sm.Mint("alice", "alice@example.com", "oidc", []string{"developers", "admins"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	claims, err := sm.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if claims.Subject != "alice" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "alice")
	}
	if claims.Email != "alice@example.com" {
		t.Errorf("Email = %q, want %q", claims.Email, "alice@example.com")
	}
	if claims.Provider != "oidc" {
		t.Errorf("Provider = %q, want %q", claims.Provider, "oidc")
	}
	if len(claims.Groups) != 2 || claims.Groups[0] != "developers" {
		t.Errorf("Groups = %v, want [developers admins]", claims.Groups)
	}
	if claims.Issuer != "diverge-server" {
		t.Errorf("Issuer = %q, want %q", claims.Issuer, "diverge-server")
	}
}

func TestSessionManager_VerifyExpired(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	sm, err := NewSessionManager(SessionConfig{
		SigningKey: key,
		MaxAge:     -1 * time.Hour, // Already expired
	})
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	token, err := sm.Mint("alice", "alice@example.com", "oidc", nil)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	_, err = sm.Verify(token)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error = %q, want to contain 'expired'", err.Error())
	}
}

func TestSessionManager_VerifyTampered(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	sm, err := NewSessionManager(SessionConfig{
		SigningKey: key,
		MaxAge:     1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	token, err := sm.Mint("alice", "alice@example.com", "oidc", nil)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	// Tamper with the token
	tampered := token + "x"
	_, err = sm.Verify(tampered)
	if err == nil {
		t.Fatal("expected error for tampered token, got nil")
	}
}

func TestSessionManager_VerifyWrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	for i := range key1 {
		key1[i] = byte(i)
		key2[i] = byte(i + 1)
	}

	sm1, _ := NewSessionManager(SessionConfig{SigningKey: key1, MaxAge: 1 * time.Hour})
	sm2, _ := NewSessionManager(SessionConfig{SigningKey: key2, MaxAge: 1 * time.Hour})

	token, _ := sm1.Mint("alice", "", "oidc", nil)

	_, err := sm2.Verify(token)
	if err == nil {
		t.Fatal("expected error verifying with wrong key, got nil")
	}
	if !strings.Contains(err.Error(), "signature") {
		t.Errorf("error = %q, want to contain 'signature'", err.Error())
	}
}

func TestSessionManager_InvalidFormat(t *testing.T) {
	key := make([]byte, 32)
	sm, _ := NewSessionManager(SessionConfig{SigningKey: key, MaxAge: 1 * time.Hour})

	tests := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"no dot", "nodot"},
		{"just dots", "."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sm.Verify(tt.token)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestNewSessionManager_ShortKey(t *testing.T) {
	_, err := NewSessionManager(SessionConfig{
		SigningKey: []byte("too-short"),
	})
	if err == nil {
		t.Fatal("expected error for short key, got nil")
	}
}

func TestNewSessionManager_AutoGenerateKey(t *testing.T) {
	sm, err := NewSessionManager(SessionConfig{})
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	token, err := sm.Mint("bob", "", "github", nil)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	claims, err := sm.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "bob" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "bob")
	}
}

func TestGenerateKey(t *testing.T) {
	keyStr, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(keyStr)
	if err != nil {
		t.Fatalf("invalid base64: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("key length = %d, want 32", len(decoded))
	}
}

func TestSessionManager_IssuerMismatch(t *testing.T) {
	key := make([]byte, 32)
	sm1, _ := NewSessionManager(SessionConfig{SigningKey: key, Issuer: "issuer1"})
	sm2, _ := NewSessionManager(SessionConfig{SigningKey: key, Issuer: "issuer2"})

	token, _ := sm1.Mint("alice", "a@example.com", "oidc", nil)

	_, err := sm2.Verify(token)
	if err == nil {
		t.Fatal("expected error for issuer mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "issuer") {
		t.Errorf("expected error to mention issuer, got %v", err)
	}
}

func TestSessionManager_DefaultValues(t *testing.T) {
	sm, _ := NewSessionManager(SessionConfig{})
	if sm.config.Issuer != "diverge-server" {
		t.Errorf("expected default issuer diverge-server, got %q", sm.config.Issuer)
	}
	if sm.config.MaxAge != 24*time.Hour {
		t.Errorf("expected default max age 24h, got %v", sm.config.MaxAge)
	}
}

func TestSessionManager_MintVerify_PBT(t *testing.T) {
	key := make([]byte, 32)
	sm, _ := NewSessionManager(SessionConfig{SigningKey: key, MaxAge: time.Hour})

	rapid.Check(t, func(t *rapid.T) {
		subject := rapid.String().Draw(t, "subject")
		email := rapid.String().Draw(t, "email")
		provider := rapid.String().Draw(t, "provider")
		groups := rapid.SliceOf(rapid.String()).Draw(t, "groups")

		token, err := sm.Mint(subject, email, provider, groups)
		if err != nil {
			t.Fatalf("Mint error: %v", err)
		}

		claims, err := sm.Verify(token)
		if err != nil {
			t.Fatalf("Verify error: %v", err)
		}

		if claims.Subject != subject {
			t.Errorf("subject mismatch")
		}
		if claims.Email != email {
			t.Errorf("email mismatch")
		}
		if claims.Provider != provider {
			t.Errorf("provider mismatch")
		}

		if len(groups) == 0 {
			if len(claims.Groups) != 0 {
				t.Errorf("groups mismatch")
			}
		} else {
			if len(claims.Groups) != len(groups) {
				t.Fatalf("groups length mismatch")
			}
			for i, g := range groups {
				if claims.Groups[i] != g {
					t.Errorf("groups mismatch at %d", i)
				}
			}
		}
	})
}

func TestSessionManager_TamperProof_PBT(t *testing.T) {
	key := make([]byte, 32)
	sm, _ := NewSessionManager(SessionConfig{SigningKey: key, MaxAge: time.Hour})

	rapid.Check(t, func(t *rapid.T) {
		subject := rapid.String().Draw(t, "subject")
		token, _ := sm.Mint(subject, "", "test", nil)

		tokenBytes := []byte(token)
		idx := rapid.IntRange(0, len(tokenBytes)-1).Draw(t, "idx")

		// Flip a bit
		tokenBytes[idx] ^= 1

		tampered := string(tokenBytes)
		_, err := sm.Verify(tampered)
		if err == nil {
			t.Fatalf("expected tampered token to fail verification")
		}
	})
}
