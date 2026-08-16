package cli

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextCommands(t *testing.T) {
	tmpHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer func() { _ = os.Setenv("HOME", originalHome) }()
	_ = os.Setenv("HOME", tmpHome)

	require.NoError(t, saveCredentials("server1", "token1", "", time.Time{}))
	require.NoError(t, saveCredentials("server2", "token2", "", time.Time{}))

	app := &App{}

	listCmd := newContextListCmd(app)
	var listBuf bytes.Buffer
	listCmd.SetOut(&listBuf)
	require.NoError(t, listCmd.RunE(listCmd, []string{}))
	out := listBuf.String()
	assert.Contains(t, out, "server1")
	assert.Contains(t, out, "server2")
	assert.Contains(t, out, "*") // active because it was saved last

	useCmd := newContextUseCmd(app)
	require.NoError(t, useCmd.RunE(useCmd, []string{"server1"}))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "server1", cfg.ActiveContext)

	deleteCmd := newContextDeleteCmd(app)
	require.NoError(t, deleteCmd.RunE(deleteCmd, []string{"server1"}))

	cfg2, err := LoadConfig()
	require.NoError(t, err)
	assert.NotContains(t, cfg2.Contexts, "server1")
	assert.Empty(t, cfg2.ActiveContext)
}
