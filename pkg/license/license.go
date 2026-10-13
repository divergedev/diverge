package license

import (
	"crypto/ed25519"
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
	FeatureConflictDetection  = "conflict_detection"
	FeaturePortForward        = "port_forward"
	FeatureVisualVerification = "visual_verification"
	FeatureCommercialFlags    = "commercial_flags"
	FeatureAgentWarmPool      = "agent_warm_pool"
	FeatureSSO                = "sso"
	FeatureAuditLogging       = "audit_logging"
	FeatureFinOpsGovernance   = "finops_governance"
	FeatureMultiCluster       = "multi_cluster"
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

// ValidationResult describes the verified claims and grace-period state of a license token.
type ValidationResult struct {
	Claims
	IsGrace       bool
	InGracePeriod bool
	GraceUntil    time.Time
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

type tokenHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// SignEd25519 creates an Ed25519 (EdDSA) signed JWT-style token string for the given claims.
func SignEd25519(claims Claims, privKey ed25519.PrivateKey) (string, error) {
	if len(privKey) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("%w: invalid ed25519 private key length %d", ErrInvalidSignature, len(privKey))
	}

	headerJSON, _ := json.Marshal(map[string]string{
		"alg": "EdDSA",
		"typ": "JWT",
	})
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshaling claims: %w", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	signingInput := headerB64 + "." + payloadB64
	sig := ed25519.Sign(privKey, []byte(signingInput))
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return signingInput + "." + sigB64, nil
}

// ParseAndValidateEd25519 parses an EdDSA token string and verifies its Ed25519 signature and expiration.
func ParseAndValidateEd25519(token string, pubKey ed25519.PublicKey, now time.Time) (*ValidationResult, error) {
	if len(pubKey) == ed25519.PrivateKeySize {
		pubKey = ed25519.PrivateKey(pubKey).Public().(ed25519.PublicKey)
	}
	if len(pubKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidSignature
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var hdr tokenHeader
	if err := json.Unmarshal(headerJSON, &hdr); err != nil {
		return nil, ErrInvalidToken
	}
	if hdr.Alg != "EdDSA" {
		return nil, ErrInvalidSignature
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrInvalidToken
	}

	signingInput := parts[0] + "." + parts[1]
	if len(sigBytes) != ed25519.SignatureSize || !ed25519.Verify(pubKey, []byte(signingInput), sigBytes) {
		return nil, ErrInvalidSignature
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}

	var claims Claims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("unmarshaling payload: %w", err)
	}

	if now.IsZero() {
		now = time.Now()
	}
	isGrace := false
	var graceUntil time.Time
	if !claims.ExpiresAt.IsZero() {
		graceUntil = claims.ExpiresAt.Add(GracePeriod)
		if now.After(claims.ExpiresAt) {
			if now.Before(graceUntil) {
				isGrace = true
			} else {
				return &ValidationResult{
					Claims:     claims,
					GraceUntil: graceUntil,
				}, ErrLicenseExpired
			}
		}
	}

	return &ValidationResult{
		Claims:        claims,
		IsGrace:       isGrace,
		InGracePeriod: isGrace,
		GraceUntil:    graceUntil,
	}, nil
}

// Sign creates a signed JWT-style token string for the given claims.
// If secret is a 64-byte Ed25519 private key, it signs using EdDSA; otherwise it signs using HMAC-SHA256 (HS256).
func Sign(claims Claims, secret []byte) (string, error) {
	if len(secret) == ed25519.PrivateKeySize {
		return SignEd25519(claims, ed25519.PrivateKey(secret))
	}
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

// ParseAndValidate parses a token string and verifies its signature (EdDSA or HS256) and expiration.
func ParseAndValidate(token string, secret []byte, now ...time.Time) (*Claims, bool, error) {
	evalNow := time.Now()
	if len(now) > 0 && !now[0].IsZero() {
		evalNow = now[0]
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false, ErrInvalidToken
	}

	if headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0]); err == nil {
		var hdr tokenHeader
		if err := json.Unmarshal(headerJSON, &hdr); err == nil && hdr.Alg == "EdDSA" {
			res, err := ParseAndValidateEd25519(token, ed25519.PublicKey(secret), evalNow)
			if res != nil {
				c := res.Claims
				return &c, res.IsGrace, err
			}
			return nil, false, err
		}
	}

	if len(secret) == 0 {
		secret = DefaultSecret
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

	isGrace := false

	if !claims.ExpiresAt.IsZero() && evalNow.After(claims.ExpiresAt) {
		if evalNow.Before(claims.ExpiresAt.Add(GracePeriod)) {
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

// IsFeatureAllowed reports whether the given feature key is enabled for the specified license tier.
func IsFeatureAllowed(tier Tier, feature string) bool {
	if tier == TierCommunity && feature == FeatureConflictDetection {
		return true
	}
	return hasFeature(featuresForTier(tier), feature)
}

func featuresForTier(tier Tier) []string {
	switch tier {
	case TierEnterprise:
		return []string{
			FeatureConflictDetection,
			FeaturePortForward,
			FeatureVisualVerification,
			FeatureCommercialFlags,
			FeatureAgentWarmPool,
			FeatureSSO,
			FeatureAuditLogging,
			FeatureFinOpsGovernance,
			FeatureMultiCluster,
		}
	case TierPro:
		return []string{
			FeatureConflictDetection,
			FeaturePortForward,
			FeatureVisualVerification,
			FeatureCommercialFlags,
			FeatureAgentWarmPool,
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
