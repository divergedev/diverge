package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SessionConfig configures session JWT creation and verification.
type SessionConfig struct {
	// SigningKey is the HMAC-SHA256 key for signing session JWTs.
	SigningKey []byte
	// MaxAge is the session duration. Defaults to 24 hours.
	MaxAge time.Duration
	// Issuer is the JWT issuer claim. Defaults to "diverge-server".
	Issuer string
}

// SessionClaims represents the claims in a Diverge session JWT.
type SessionClaims struct {
	Subject  string   `json:"sub"`
	Email    string   `json:"email,omitempty"`
	Groups   []string `json:"groups,omitempty"`
	Issuer   string   `json:"iss"`
	IssuedAt int64    `json:"iat"`
	Expiry   int64    `json:"exp"`
	// Provider tracks which auth method created this session (e.g. "oidc", "github").
	Provider string `json:"provider,omitempty"`
}

// SessionManager handles creation and verification of signed session JWTs.
type SessionManager struct {
	config SessionConfig
}

// NewSessionManager creates a new session manager with the given config.
// If SigningKey is empty, a random 32-byte key is generated (sessions won't
// survive server restarts).
func NewSessionManager(cfg SessionConfig) (*SessionManager, error) {
	if len(cfg.SigningKey) == 0 {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("failed to generate session signing key: %w", err)
		}
		cfg.SigningKey = key
	}
	if len(cfg.SigningKey) < 32 {
		return nil, errors.New("session signing key must be at least 32 bytes")
	}
	if cfg.MaxAge == 0 {
		cfg.MaxAge = 24 * time.Hour
	}
	if cfg.Issuer == "" {
		cfg.Issuer = "diverge-server"
	}
	return &SessionManager{config: cfg}, nil
}

// GenerateKey generates a cryptographically random 32-byte key suitable for
// use as a session signing key. Returns the key as base64-encoded string.
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("failed to generate key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

// Mint creates a signed session JWT for the given user identity.
func (sm *SessionManager) Mint(subject, email, provider string, groups []string) (string, error) {
	now := time.Now()
	claims := SessionClaims{
		Subject:  subject,
		Email:    email,
		Groups:   groups,
		Issuer:   sm.config.Issuer,
		IssuedAt: now.Unix(),
		Expiry:   now.Add(sm.config.MaxAge).Unix(),
		Provider: provider,
	}

	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("failed to marshal session claims: %w", err)
	}

	// header.payload.signature (simplified JWT without the header — we only support HS256)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	sig := sm.sign(encoded)
	return encoded + "." + sig, nil
}

// Verify validates a session token and returns the claims.
func (sm *SessionManager) Verify(token string) (*SessionClaims, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, errors.New("invalid session token format")
	}
	payload, sig := parts[0], parts[1]

	// Verify signature
	expectedSig := sm.sign(payload)
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return nil, errors.New("invalid session token signature")
	}

	// Decode claims
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode session token: %w", err)
	}

	var claims SessionClaims
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session claims: %w", err)
	}

	// Check expiry
	if time.Now().Unix() > claims.Expiry {
		return nil, errors.New("session token expired")
	}

	// Check issuer
	if claims.Issuer != sm.config.Issuer {
		return nil, fmt.Errorf("invalid session issuer: got %q, want %q", claims.Issuer, sm.config.Issuer)
	}

	return &claims, nil
}

func (sm *SessionManager) sign(data string) string {
	mac := hmac.New(sha256.New, sm.config.SigningKey)
	mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
