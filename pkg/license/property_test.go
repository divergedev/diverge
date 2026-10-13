package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
)

func TestLicense_SignAndValidateRoundTrip_PBT(t *testing.T) {
	// Generate key material once outside the property loop.
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	otherPubKey, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	secret := []byte(privKey)

	hegel.Test(t, func(ht *hegel.T) {
		tier := hegel.Draw(ht, hegel.SampledFrom([]Tier{TierCommunity, TierPro, TierEnterprise}))
		org := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(60))
		seats := hegel.Draw(ht, hegel.Integers(1, 5000))
		futureHours := hegel.Draw(ht, hegel.Integers(1, 24*365))

		now := time.Now().UTC().Truncate(time.Second)
		claims := Claims{
			Tier:      tier,
			Org:       org,
			Seats:     seats,
			ExpiresAt: now.Add(time.Duration(futureHours) * time.Hour),
			IssuedAt:  now,
		}

		// 1. Valid future license round-trip
		tok, err := Sign(claims, secret)
		require.NoError(ht, err)

		parsed, isGrace, err := ParseAndValidate(tok, secret)
		require.NoError(ht, err)
		require.False(ht, isGrace)
		require.Equal(ht, claims.Tier, parsed.Tier)
		require.Equal(ht, claims.Org, parsed.Org)
		require.Equal(ht, claims.Seats, parsed.Seats)
		require.True(ht, claims.ExpiresAt.Equal(parsed.ExpiresAt))

		edRes, err := ParseAndValidateEd25519(tok, pubKey, now)
		require.NoError(ht, err)
		require.False(ht, edRes.IsGrace)
		require.Equal(ht, claims.Tier, edRes.Tier)
		require.Equal(ht, claims.Org, edRes.Org)
		require.Equal(ht, claims.Seats, edRes.Seats)

		// Wrong public key is rejected
		_, err = ParseAndValidateEd25519(tok, otherPubKey, now)
		require.True(ht, errors.Is(err, ErrInvalidSignature))

		// 2. Grace period: ExpiresAt between -6 days and -1 minute -> Valid (err == nil) && isGrace == true
		graceMinutesAgo := hegel.Draw(ht, hegel.Integers(1, 6*24*60))
		graceClaims := claims
		graceClaims.ExpiresAt = time.Now().Add(-time.Duration(graceMinutesAgo) * time.Minute)

		graceTok, err := Sign(graceClaims, secret)
		require.NoError(ht, err)

		graceParsed, inGrace, err := ParseAndValidate(graceTok, secret)
		require.NoError(ht, err)
		require.NotNil(ht, graceParsed)
		require.True(ht, inGrace, "expected license expired %d minutes ago to be in grace period", graceMinutesAgo)

		// 3. Expired past grace period: ExpiresAt < -8 days -> ErrLicenseExpired
		expiredDaysAgo := hegel.Draw(ht, hegel.Integers(8, 365))
		expiredClaims := claims
		expiredClaims.ExpiresAt = time.Now().Add(-time.Duration(expiredDaysAgo) * 24 * time.Hour)

		expiredTok, err := Sign(expiredClaims, secret)
		require.NoError(ht, err)

		_, _, err = ParseAndValidate(expiredTok, secret)
		require.True(ht, errors.Is(err, ErrLicenseExpired), "expected ErrLicenseExpired, got %v", err)

		// 4. Signature bit-flip rejection
		parts := strings.Split(tok, ".")
		require.Len(ht, parts, 3)
		sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
		require.NoError(ht, err)
		require.NotEmpty(ht, sigBytes)

		flipIdx := hegel.Draw(ht, hegel.Integers(0, len(sigBytes)-1))
		sigBytes[flipIdx] ^= 0x01
		tamperedTok := parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(sigBytes)

		_, _, err = ParseAndValidate(tamperedTok, secret)
		require.True(ht, errors.Is(err, ErrInvalidSignature), "expected ErrInvalidSignature on bit flip, got %v", err)
	})
}
