package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitCreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	originalWD, err := os.Getwd()
	require.NoError(t, err)
	defer func() {
		err := os.Chdir(originalWD)
		require.NoError(t, err)
	}()

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	app := &App{}
	cmd := newInitCmd(app)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})

	err = cmd.Execute()
	require.NoError(t, err)

	content, err := os.ReadFile(".diverge.yaml")
	require.NoError(t, err)

	yamlStr := string(content)
	assert.Contains(t, yamlStr, "version: \"1\"")
	assert.Contains(t, yamlStr, "services:")
	assert.Contains(t, yamlStr, "deploy:")
}

func TestInitWontOverwriteWithoutConfirm(t *testing.T) {
	tmpDir := t.TempDir()
	originalWD, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(originalWD) })

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	err = os.WriteFile(".diverge.yaml", []byte("existing content"), 0644)
	require.NoError(t, err)

	app := &App{}
	cmd := newInitCmd(app)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetIn(strings.NewReader("n\n"))
	cmd.SetArgs([]string{})

	err = cmd.Execute()
	require.NoError(t, err)

	content, err := os.ReadFile(".diverge.yaml")
	require.NoError(t, err)
	assert.Equal(t, "existing content", string(content))
}
