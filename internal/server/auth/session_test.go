package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
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
