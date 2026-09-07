package license

import (
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
