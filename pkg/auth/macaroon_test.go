package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMacaroonMintAndVerify(t *testing.T) {
	ctx := context.Background()
	rootKey := []byte("0123456789abcdef0123456789abcdef")
	provider := NewMacaroonProvider(rootKey)

	claims := Claims{
		TaskID:        "task-123",
		Namespace:     "default",
		RepoURL:       "https://github.com/org/repo",
		AllowedTools:  []string{"diverge_*", "test_runner"},
		AllowedModels: []string{"gemini-2.5-pro", "gemini-2.5-flash"},
		MaxCostUSD:    10.00,
		ExpiresAt:     time.Now().Add(1 * time.Hour),
	}

	tok, err := provider.Mint(ctx, claims)
	require.NoError(t, err)
	require.NotNil(t, tok)

	raw, err := tok.Serialize()
	require.NoError(t, err)

	// Verify valid token
	err = provider.Verify(ctx, raw, Claims{
		TaskID:        "task-123",
		Namespace:     "default",
		RepoURL:       "https://github.com/org/repo",
		AllowedTools:  []string{"diverge_create_preview"},
		AllowedModels: []string{"gemini-2.5-flash"},
		MaxCostUSD:    5.00,
	})
	assert.NoError(t, err)
}

func TestMacaroonTamperDetection(t *testing.T) {
	ctx := context.Background()
	rootKey := []byte("0123456789abcdef0123456789abcdef")
	provider := NewMacaroonProvider(rootKey)

	claims := Claims{
		TaskID:    "task-456",
		Namespace: "default",
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	tok, err := provider.Mint(ctx, claims)
	require.NoError(t, err)

	mac := tok.(*Macaroon)
	// Tamper with caveat
	mac.CaveatSeq[0].Value = "tampered-task-id"
	raw, err := mac.Serialize()
	require.NoError(t, err)

	err = provider.Verify(ctx, raw, Claims{TaskID: "task-456"})
	assert.Error(t, err)
}

func TestMacaroonAttenuation(t *testing.T) {
	ctx := context.Background()
	rootKey := []byte("0123456789abcdef0123456789abcdef")
	provider := NewMacaroonProvider(rootKey)

	claims := Claims{
		TaskID:       "task-789",
		Namespace:    "default",
		AllowedTools: []string{"diverge_*"},
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}

	rootTok, err := provider.Mint(ctx, claims)
	require.NoError(t, err)

	// Attenuate to only read-only tools
	attenuated, err := provider.Attenuate(ctx, rootTok, Caveat{
		Key:   "allowed_tools",
		Op:    OpGlob,
		Value: "diverge_get_*,diverge_list_*",
	})
	require.NoError(t, err)

	raw, err := attenuated.Serialize()
	require.NoError(t, err)

	// Allowed
	err = provider.Verify(ctx, raw, Claims{
		TaskID:       "task-789",
		AllowedTools: []string{"diverge_get_environment"},
	})
	assert.NoError(t, err)

	// Disallowed tool
	err = provider.Verify(ctx, raw, Claims{
		TaskID:       "task-789",
		AllowedTools: []string{"diverge_delete_environment"},
	})
	assert.Error(t, err)
}

func TestMacaroonExpired(t *testing.T) {
	ctx := context.Background()
	rootKey := []byte("0123456789abcdef0123456789abcdef")
	provider := NewMacaroonProvider(rootKey)

	claims := Claims{
		TaskID:    "task-expired",
		Namespace: "default",
		ExpiresAt: time.Now().Add(-1 * time.Minute), // in the past
	}

	tok, err := provider.Mint(ctx, claims)
	require.NoError(t, err)

	raw, err := tok.Serialize()
	require.NoError(t, err)

	err = provider.Verify(ctx, raw, Claims{TaskID: "task-expired"})
	assert.ErrorIs(t, err, ErrTokenExpired)
}

func TestMacaroonFailClosed(t *testing.T) {
	ctx := context.Background()
	rootKey := []byte("0123456789abcdef0123456789abcdef")
	provider := NewMacaroonProvider(rootKey)

	claims := Claims{
		TaskID:        "task-fail-closed",
		Namespace:     "default",
		RepoURL:       "https://github.com/org/repo",
		AllowedTools:  []string{"diverge_get_*"},
		AllowedModels: []string{"gemini-2.5-flash"},
	}

	tok, err := provider.Mint(ctx, claims)
	require.NoError(t, err)

	raw, err := tok.Serialize()
	require.NoError(t, err)

	// Fails if request claims are missing required allowed_tools
	err = provider.Verify(ctx, raw, Claims{
		TaskID:        "task-fail-closed",
		RepoURL:       "https://github.com/org/repo",
		AllowedModels: []string{"gemini-2.5-flash"},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no tool was specified")

	// Fails if request claims are missing required allowed_models
	err = provider.Verify(ctx, raw, Claims{
		TaskID:       "task-fail-closed",
		RepoURL:      "https://github.com/org/repo",
		AllowedTools: []string{"diverge_get_environment"},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no model was specified")
}

func TestMacaroonTokenLimits(t *testing.T) {
	ctx := context.Background()
	rootKey := []byte("0123456789abcdef0123456789abcdef")
	provider := NewMacaroonProvider(rootKey)

	claims := Claims{
		TaskID:    "task-tokens",
		Namespace: "default",
		MaxTokens: 500000,
	}

	tok, err := provider.Mint(ctx, claims)
	require.NoError(t, err)

	raw, err := tok.Serialize()
	require.NoError(t, err)

	// Under budget passes
	err = provider.Verify(ctx, raw, Claims{
		TaskID:         "task-tokens",
		TokensConsumed: 250000,
	})
	assert.NoError(t, err)

	// Over budget fails
	err = provider.Verify(ctx, raw, Claims{
		TaskID:         "task-tokens",
		TokensConsumed: 550000,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds max limit")
}

func TestMacaroonBranchCaveat(t *testing.T) {
	ctx := context.Background()
	rootKey := []byte("0123456789abcdef0123456789abcdef")
	provider := NewMacaroonProvider(rootKey)

	claims := Claims{
		TaskID:    "task-branch",
		Namespace: "default",
		RepoURL:   "https://github.com/org/repo",
		Branch:    "feat/payments",
	}

	tok, err := provider.Mint(ctx, claims)
	require.NoError(t, err)

	raw, err := tok.Serialize()
	require.NoError(t, err)

	// Correct branch passes
	err = provider.Verify(ctx, raw, Claims{
		TaskID:  "task-branch",
		RepoURL: "https://github.com/org/repo",
		Branch:  "feat/payments",
	})
	assert.NoError(t, err)

	// Different branch fails
	err = provider.Verify(ctx, raw, Claims{
		TaskID:  "task-branch",
		RepoURL: "https://github.com/org/repo",
		Branch:  "main",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "branch mismatch")
}

func TestMacaroonOperatorAndUnknownKeyValidation(t *testing.T) {
	ctx := context.Background()
	rootKey := []byte("0123456789abcdef0123456789abcdef")
	provider := NewMacaroonProvider(rootKey)

	tok, err := provider.Mint(ctx, Claims{TaskID: "task-test", Namespace: "default"})
	require.NoError(t, err)

	// Attenuate with unknown caveat key
	attenuated, err := provider.Attenuate(ctx, tok, Caveat{
		Key:   "custom_unsupported_key",
		Op:    OpEqual,
		Value: "value",
	})
	require.NoError(t, err)

	raw, err := attenuated.Serialize()
	require.NoError(t, err)

	err = provider.Verify(ctx, raw, Claims{TaskID: "task-test"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unrecognized caveat key")

	// Attenuate with invalid operator for allowed_tools
	attenuatedOp, err := provider.Attenuate(ctx, tok, Caveat{
		Key:   "allowed_tools",
		Op:    OpEqual, // expected OpGlob
		Value: "diverge_*",
	})
	require.NoError(t, err)

	rawOp, err := attenuatedOp.Serialize()
	require.NoError(t, err)

	err = provider.Verify(ctx, rawOp, Claims{TaskID: "task-test", AllowedTools: []string{"diverge_test"}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid operator")
}

func TestMacaroonNonceUniqueness(t *testing.T) {
	ctx := context.Background()
	rootKey := []byte("0123456789abcdef0123456789abcdef")
	provider := NewMacaroonProvider(rootKey)

	claims := Claims{
		TaskID:    "identical-task-id",
		Namespace: "default",
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	tok1, err := provider.Mint(ctx, claims)
	require.NoError(t, err)

	tok2, err := provider.Mint(ctx, claims)
	require.NoError(t, err)

	mac1 := tok1.(*Macaroon)
	mac2 := tok2.(*Macaroon)

	// Even with identical claims, signatures and nonces must differ
	assert.NotEqual(t, mac1.Signature, mac2.Signature)

	var nonce1, nonce2 string
	for _, c := range mac1.CaveatSeq {
		if c.Key == "nonce" {
			nonce1 = c.Value
		}
	}
	for _, c := range mac2.CaveatSeq {
		if c.Key == "nonce" {
			nonce2 = c.Value
		}
	}
	assert.NotEmpty(t, nonce1)
	assert.NotEmpty(t, nonce2)
	assert.NotEqual(t, nonce1, nonce2)

	// Both should verify successfully
	raw1, err := tok1.Serialize()
	require.NoError(t, err)
	assert.NoError(t, provider.Verify(ctx, raw1, Claims{TaskID: "identical-task-id"}))

	raw2, err := tok2.Serialize()
	require.NoError(t, err)
	assert.NoError(t, provider.Verify(ctx, raw2, Claims{TaskID: "identical-task-id"}))
}

func TestMacaroonVerifyAndExtract(t *testing.T) {
	ctx := context.Background()
	rootKey := []byte("0123456789abcdef0123456789abcdef")
	provider := NewMacaroonProvider(rootKey)

	claims := Claims{
		TaskID:        "task-extract",
		Namespace:     "team-alpha",
		RepoURL:       "https://github.com/org/repo",
		Branch:        "main",
		AllowedTools:  []string{"diverge_*"},
		AllowedModels: []string{"gemini-2.5-pro", "gemini-2.5-flash"},
		MaxCostUSD:    12.50,
		MaxTokens:     200000,
		ExpiresAt:     time.Now().Add(1 * time.Hour),
	}

	tok, err := provider.Mint(ctx, claims)
	require.NoError(t, err)

	// Narrow via attenuation
	attenuated, err := provider.Attenuate(ctx, tok,
		Caveat{Key: "allowed_tools", Op: OpGlob, Value: "diverge_get_*"},
		Caveat{Key: "max_cost_usd", Op: OpLessThan, Value: "5.00"},
	)
	require.NoError(t, err)

	raw, err := attenuated.Serialize()
	require.NoError(t, err)

	mac, extracted, err := provider.VerifyAndExtract(ctx, raw)
	require.NoError(t, err)
	require.NotNil(t, mac)
	assert.Equal(t, "task-extract", extracted.TaskID)
	assert.Equal(t, "team-alpha", extracted.Namespace)
	assert.Equal(t, "https://github.com/org/repo", extracted.RepoURL)
	assert.Equal(t, "main", extracted.Branch)
	assert.Equal(t, []string{"diverge_get_*"}, extracted.AllowedTools)
	assert.InDelta(t, 5.00, extracted.MaxCostUSD, 0.001)
	assert.Equal(t, int64(200000), extracted.MaxTokens)

	// Attempting to widen allowed_tools via attenuation must fail closed
	widened, err := provider.Attenuate(ctx, attenuated,
		Caveat{Key: "allowed_tools", Op: OpGlob, Value: "kubectl_*"},
	)
	require.NoError(t, err)
	widenedRaw, err := widened.Serialize()
	require.NoError(t, err)
	_, _, err = provider.VerifyAndExtract(ctx, widenedRaw)
	assert.ErrorIs(t, err, ErrCaveatFailed)

	// Conflicting namespace caveat must fail closed
	conflictingNS, err := provider.Attenuate(ctx, tok,
		Caveat{Key: "namespace", Op: OpEqual, Value: "team-beta"},
	)
	require.NoError(t, err)
	conflictRaw, err := conflictingNS.Serialize()
	require.NoError(t, err)
	_, _, err = provider.VerifyAndExtract(ctx, conflictRaw)
	assert.ErrorIs(t, err, ErrCaveatFailed)

	// Non-positive max_cost_usd or max_tokens caveats must fail closed
	for _, badCaveat := range []Caveat{
		{Key: "max_cost_usd", Op: OpLessThan, Value: "0"},
		{Key: "max_cost_usd", Op: OpLessThan, Value: "-5.00"},
		{Key: "max_cost_usd", Op: OpLessThan, Value: "NaN"},
		{Key: "max_cost_usd", Op: OpLessThan, Value: "+Inf"},
		{Key: "max_tokens", Op: OpLessThan, Value: "0"},
		{Key: "max_tokens", Op: OpLessThan, Value: "-100"},
	} {
		badTok, err := provider.Attenuate(ctx, tok, badCaveat)
		require.NoError(t, err)
		badRaw, err := badTok.Serialize()
		require.NoError(t, err)
		_, _, err = provider.VerifyAndExtract(ctx, badRaw)
		assert.ErrorIs(t, err, ErrCaveatFailed, "expected ErrCaveatFailed for %+v", badCaveat)
	}
}
