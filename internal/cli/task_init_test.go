package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskInitAndLoad(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "task-init-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tempDir) }()

	origWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tempDir))
	defer func() { _ = os.Chdir(origWd) }()

	// First init should succeed
	err = runTaskInit(false)
	require.NoError(t, err)

	cfgPath := filepath.Join(tempDir, ".diverge", "agent.yaml")
	assert.FileExists(t, cfgPath)

	// Second init without force should fail
	err = runTaskInit(false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// Init with force should succeed
	err = runTaskInit(true)
	assert.NoError(t, err)

	// Test loading
	cfg, err := LoadAgentRepoConfig(tempDir)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "v1", cfg.Version)
	assert.Contains(t, cfg.Agent.AllowedModels, "claude-3-5-sonnet")
	assert.Contains(t, cfg.Agent.AllowedTools, "git")
	assert.Equal(t, "5.00", cfg.Agent.BudgetUSD)
	assert.Equal(t, int32(5), cfg.Agent.MaxIterations)
	assert.Equal(t, "main", cfg.Agent.BaseBranch)
	assert.Equal(t, "agent-sandbox", cfg.Agent.Sandbox.Provider)
	assert.Equal(t, int32(3600), cfg.Agent.Sandbox.TimeoutSeconds)
}

func TestLoadAgentRepoConfigNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "task-init-none-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tempDir) }()

	cfg, err := LoadAgentRepoConfig(tempDir)
	assert.NoError(t, err)
	assert.Nil(t, cfg)
}
