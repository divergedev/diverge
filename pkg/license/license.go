package license

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Tier represents the licensing tier of Diverge.
type Tier string

const (
	TierCommunity  Tier = "community"
	TierPro        Tier = "pro"
	TierEnterprise Tier = "enterprise"
)

// Supported feature keys.
const (
	FeatureConflictDetection = "conflict_detection"
	FeaturePortForward       = "port_forward"
	FeatureSSO               = "sso"
	FeatureAuditLogging      = "audit_logging"
)

// GracePeriod is the allowed duration after expiration before features are locked.
const GracePeriod = 7 * 24 * time.Hour

// DefaultSecret is the public verification HMAC secret for standard license tokens.
// In enterprise self-hosted setups, this can be overridden via DIVERGE_LICENSE_SECRET.
var DefaultSecret = []byte("diverge-commercial-license-hmac-v1")

var (
	ErrInvalidToken        = errors.New("invalid license token format")
	ErrInvalidSignature    = errors.New("invalid license signature")
	ErrLicenseExpired      = errors.New("license has expired past grace period")
	ErrUnauthorizedFeature = errors.New("feature not included in current tier")
)

// Claims represents the payload encoded in a Diverge license key.
type Claims struct {
	Tier      Tier      `json:"tier"`
	Org       string    `json:"org"`
	Seats     int       `json:"seats,omitempty"`
	ExpiresAt time.Time `json:"exp"`
	IssuedAt  time.Time `json:"iat,omitempty"`
}

// LicenseInfo describes the verified state of a Diverge license.
type LicenseInfo struct {
	Tier           Tier
	Org            string
	ExpiresAt      time.Time
	IsTrial        bool
	IsGrace        bool
	GraceUntil     time.Time
	ActiveFeatures []string
}

// Sign creates an HMAC-SHA256 signed JWT-style token string for the given claims.
func Sign(claims Claims, secret []byte) (string, error) {
	if len(secret) == 0 {
		secret = DefaultSecret
	}

	headerJSON, _ := json.Marshal(map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	})
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshaling claims: %w", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	sigInput := headerB64 + "." + payloadB64
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sigInput))
	sigB64 := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return sigInput + "." + sigB64, nil
}

// ParseAndValidate parses a token string and verifies its HMAC signature and expiration.
func ParseAndValidate(token string, secret []byte) (*Claims, bool, error) {
	if len(secret) == 0 {
		secret = DefaultSecret
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false, ErrInvalidToken
	}

	sigInput := parts[0] + "." + parts[1]
	expectedMAC := hmac.New(sha256.New, secret)
	expectedMAC.Write([]byte(sigInput))
	expectedSig := expectedMAC.Sum(nil)

	actualSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, false, ErrInvalidToken
	}

	if !hmac.Equal(actualSig, expectedSig) {
		return nil, false, ErrInvalidSignature
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false, ErrInvalidToken
	}

	var claims Claims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, false, fmt.Errorf("unmarshaling payload: %w", err)
	}

	now := time.Now()
	isGrace := false

	if !claims.ExpiresAt.IsZero() && now.After(claims.ExpiresAt) {
		if now.Before(claims.ExpiresAt.Add(GracePeriod)) {
			isGrace = true
		} else {
			return &claims, false, ErrLicenseExpired
		}
	}

	return &claims, isGrace, nil
}

// CheckFeature verifies whether the given feature is permitted by the license token.
// If token is empty, checks DIVERGE_LICENSE_KEY.
// If still empty, returns a Community evaluation license info allowing non-blocking trial usage.
func CheckFeature(token string, secret []byte, feature string) (LicenseInfo, error) {
	if token == "" {
		token = os.Getenv("DIVERGE_LICENSE_KEY")
	}

	// No license key provided: Community Tier (evaluation)
	if token == "" {
		return LicenseInfo{
			Tier:       TierCommunity,
			Org:        "Community / Trial",
			IsTrial:    true,
			GraceUntil: time.Time{},
			ActiveFeatures: []string{
				FeatureConflictDetection, // Permitted in evaluation mode
			},
		}, nil
	}

	claims, isGrace, err := ParseAndValidate(token, secret)
	if err != nil {
		return LicenseInfo{}, err
	}

	info := LicenseInfo{
		Tier:           claims.Tier,
		Org:            claims.Org,
		ExpiresAt:      claims.ExpiresAt,
		IsGrace:        isGrace,
		GraceUntil:     claims.ExpiresAt.Add(GracePeriod),
		ActiveFeatures: featuresForTier(claims.Tier),
	}

	if !hasFeature(info.ActiveFeatures, feature) {
		return info, fmt.Errorf("%w: %q requires Pro or Enterprise tier (current: %s)", ErrUnauthorizedFeature, feature, claims.Tier)
	}

	return info, nil
}

func featuresForTier(tier Tier) []string {
	switch tier {
	case TierEnterprise:
		return []string{
			FeatureConflictDetection,
			FeaturePortForward,
			FeatureSSO,
			FeatureAuditLogging,
		}
	case TierPro:
		return []string{
			FeatureConflictDetection,
			FeaturePortForward,
		}
	default:
		return []string{}
	}
}

func hasFeature(features []string, target string) bool {
	for _, f := range features {
		if f == target {
			return true
		}
	}
	return false
}
