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
		TaskID:  "task-branch",
		RepoURL: "https://github.com/org/repo",
		Branch:  "feat/payments",
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

	tok, err := provider.Mint(ctx, Claims{TaskID: "task-test"})
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
