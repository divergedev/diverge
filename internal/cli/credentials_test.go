package cli

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigRoundtrip(t *testing.T) {
	tmpHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer func() { _ = os.Setenv("HOME", originalHome) }()
	_ = os.Setenv("HOME", tmpHome)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.ActiveContext)
	assert.NotNil(t, cfg.Contexts)

	expires := time.Now().Add(1 * time.Hour).Round(time.Second)
	err = saveCredentials("https://api.diverge.dev", "my-token", "my-refresh", expires)
	require.NoError(t, err)

	cfg2, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "https://api.diverge.dev", cfg2.ActiveContext)
	assert.Contains(t, cfg2.Contexts, "https://api.diverge.dev")
	ctx := cfg2.Contexts["https://api.diverge.dev"]
	assert.Equal(t, "https://api.diverge.dev", ctx.ServerURL)
	assert.Equal(t, "my-token", ctx.AccessToken)
	assert.Equal(t, "my-refresh", ctx.RefreshToken)
	assert.Equal(t, expires.UTC(), ctx.ExpiresAt.UTC())

	assert.Equal(t, "https://api.diverge.dev", cfg2.ActiveServerURL())
	token, err := cfg2.ActiveToken()
	require.NoError(t, err)
	assert.Equal(t, "my-token", token)
}
