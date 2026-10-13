package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignAndValidate_ValidPro(t *testing.T) {
	secret := []byte("test-secret-key-12345")
	claims := Claims{
		Tier:      TierPro,
		Org:       "Acme Corp",
		Seats:     10,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		IssuedAt:  time.Now(),
	}

	token, err := Sign(claims, secret)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	parsed, isGrace, err := ParseAndValidate(token, secret)
	require.NoError(t, err)
	assert.False(t, isGrace)
	assert.Equal(t, TierPro, parsed.Tier)
	assert.Equal(t, "Acme Corp", parsed.Org)
	assert.Equal(t, 10, parsed.Seats)

	info, err := CheckFeature(token, secret, FeatureConflictDetection)
	require.NoError(t, err)
	assert.Equal(t, TierPro, info.Tier)
	assert.False(t, info.IsTrial)
	assert.False(t, info.IsGrace)
}

func TestSignAndValidate_GracePeriod(t *testing.T) {
	secret := []byte("test-secret-key-12345")
	// Expired 2 days ago (within 7-day grace period)
	claims := Claims{
		Tier:      TierPro,
		Org:       "Acme Corp",
		ExpiresAt: time.Now().Add(-2 * 24 * time.Hour),
	}

	token, err := Sign(claims, secret)
	require.NoError(t, err)

	parsed, isGrace, err := ParseAndValidate(token, secret)
	require.NoError(t, err)
	assert.True(t, isGrace)
	assert.Equal(t, TierPro, parsed.Tier)

	info, err := CheckFeature(token, secret, FeatureConflictDetection)
	require.NoError(t, err)
	assert.True(t, info.IsGrace)
}

func TestSignAndValidate_ExpiredBeyondGrace(t *testing.T) {
	secret := []byte("test-secret-key-12345")
	// Expired 10 days ago (beyond 7-day grace period)
	claims := Claims{
		Tier:      TierPro,
		Org:       "Acme Corp",
		ExpiresAt: time.Now().Add(-10 * 24 * time.Hour),
	}

	token, err := Sign(claims, secret)
	require.NoError(t, err)

	_, _, err = ParseAndValidate(token, secret)
	assert.ErrorIs(t, err, ErrLicenseExpired)
}

func TestSignAndValidate_TamperedSignature(t *testing.T) {
	secret := []byte("test-secret-key-12345")
	claims := Claims{
		Tier:      TierPro,
		Org:       "Acme Corp",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	token, err := Sign(claims, secret)
	require.NoError(t, err)

	// Validate with different secret
	_, _, err = ParseAndValidate(token, []byte("wrong-secret"))
	assert.ErrorIs(t, err, ErrInvalidSignature)
}

func TestCheckFeature_CommunityTrial(t *testing.T) {
	// No token provided -> Community evaluation mode
	info, err := CheckFeature("", nil, FeatureConflictDetection)
	require.NoError(t, err)
	assert.Equal(t, TierCommunity, info.Tier)
	assert.True(t, info.IsTrial)
}

func TestCheckFeature_Unauthorized(t *testing.T) {
	secret := []byte("test-secret-key-12345")
	claims := Claims{
		Tier:      TierPro,
		Org:       "Acme Corp",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	token, err := Sign(claims, secret)
	require.NoError(t, err)

	// SSO is Enterprise only, not Pro
	_, err = CheckFeature(token, secret, FeatureSSO)
	assert.ErrorIs(t, err, ErrUnauthorizedFeature)
}

func TestSignAndValidateEd25519_RoundTripAndRejections(t *testing.T) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	otherPubKey, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)
	claims := Claims{
		Tier:      TierEnterprise,
		Org:       "Enterprise Corp",
		Seats:     100,
		ExpiresAt: now.Add(30 * 24 * time.Hour),
		IssuedAt:  now,
	}

	token, err := SignEd25519(claims, privKey)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// 1. Valid Ed25519 verification via ParseAndValidateEd25519
	res, err := ParseAndValidateEd25519(token, pubKey, now)
	require.NoError(t, err)
	assert.Equal(t, TierEnterprise, res.Tier)
	assert.Equal(t, "Enterprise Corp", res.Org)
	assert.Equal(t, 100, res.Seats)
	assert.False(t, res.IsGrace)

	// 2. Auto-detection via ParseAndValidate with 32-byte public key
	parsed, isGrace, err := ParseAndValidate(token, pubKey, now)
	require.NoError(t, err)
	assert.False(t, isGrace)
	assert.Equal(t, TierEnterprise, parsed.Tier)

	// 3. Wrong public key rejection
	_, err = ParseAndValidateEd25519(token, otherPubKey, now)
	assert.ErrorIs(t, err, ErrInvalidSignature)

	// 4. Tampered payload rejection
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	tamperedClaims := claims
	tamperedClaims.Seats = 9999
	tamperedTok, err := SignEd25519(tamperedClaims, privKey)
	require.NoError(t, err)
	tamperedParts := strings.Split(tamperedTok, ".")
	forgedToken := parts[0] + "." + tamperedParts[1] + "." + parts[2]
	_, err = ParseAndValidateEd25519(forgedToken, pubKey, now)
	assert.ErrorIs(t, err, ErrInvalidSignature)

	// 5. Base64-corrupted payload / header rejection
	badPayloadToken := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"tier":"enterprise"}`)) + "." + parts[2]
	_, err = ParseAndValidateEd25519(badPayloadToken, pubKey, now)
	assert.ErrorIs(t, err, ErrInvalidSignature)
}

func TestIsFeatureAllowed_ExpandedFeatures(t *testing.T) {
	proFeatures := []string{
		FeatureConflictDetection,
		FeaturePortForward,
		FeatureVisualVerification,
		FeatureCommercialFlags,
		FeatureAgentWarmPool,
	}
	enterpriseOnlyFeatures := []string{
		FeatureSSO,
		FeatureAuditLogging,
		FeatureFinOpsGovernance,
		FeatureMultiCluster,
	}

	for _, f := range proFeatures {
		assert.True(t, IsFeatureAllowed(TierPro, f), "Pro should allow %s", f)
		assert.True(t, IsFeatureAllowed(TierEnterprise, f), "Enterprise should allow %s", f)
	}
	for _, f := range enterpriseOnlyFeatures {
		assert.False(t, IsFeatureAllowed(TierPro, f), "Pro should NOT allow %s", f)
		assert.False(t, IsFeatureAllowed(TierCommunity, f), "Community should NOT allow %s", f)
		assert.True(t, IsFeatureAllowed(TierEnterprise, f), "Enterprise should allow %s", f)
	}
	assert.False(t, IsFeatureAllowed(TierCommunity, FeatureVisualVerification))
	assert.False(t, IsFeatureAllowed(TierCommunity, FeatureCommercialFlags))
	assert.False(t, IsFeatureAllowed(TierCommunity, FeatureAgentWarmPool))
}
