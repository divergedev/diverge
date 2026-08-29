package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffCmd_JSON(t *testing.T) {
	cfg := "version: \"1\"\nservices:\n  api:\n    paths: [\"services/api\"]\n  payments:\n    paths: [\"services/payments\"]\n"
	configPath := writeTestConfig(t, cfg)

	cmd := newDiffCmd(&App{})
	cmd.SetArgs([]string{"--config", configPath, "--base", "HEAD", "--output", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := cmd.Execute()
	require.NoError(t, err)

	var res diffJSONOutput
	err = json.Unmarshal(buf.Bytes(), &res)
	require.NoError(t, err)

	assert.Equal(t, "HEAD", res.BaseRef)
	assert.NotNil(t, res.Services)
}

func TestDiffCmd_NoChanges(t *testing.T) {
	cfg := "version: \"1\"\nservices:\n  api:\n    paths: [\"services/api\"]\n"
	configPath := writeTestConfig(t, cfg)

	cmd := newDiffCmd(&App{})
	cmd.SetArgs([]string{"--config", configPath, "--base", "HEAD"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "No services changed")
}

func TestDiffCmd_InvalidOutput(t *testing.T) {
	cfg := "version: \"1\"\nservices:\n  api:\n    paths: [\"services/api\"]\n"
	configPath := writeTestConfig(t, cfg)

	cmd := newDiffCmd(&App{})
	cmd.SetArgs([]string{"--config", configPath, "--output", "yaml"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported output format")
}

func TestDiffCmd_BadBaseRef(t *testing.T) {
	cfg := "version: \"1\"\nservices:\n  api:\n    paths: [\"services/api\"]\n"
	configPath := writeTestConfig(t, cfg)

	cmd := newDiffCmd(&App{})
	cmd.SetArgs([]string{"--config", configPath, "--base", "nonexistent-ref-abc123"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "git diff")
}
