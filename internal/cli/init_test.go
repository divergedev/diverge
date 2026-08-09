package cli

import (
	"bytes"
	"os"
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
	defer func() {
		err := os.Chdir(originalWD)
		require.NoError(t, err)
	}()

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	err = os.WriteFile(".diverge.yaml", []byte("existing content"), 0644)
	require.NoError(t, err)

	app := &App{}
	cmd := newInitCmd(app)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})

	oldStdin := os.Stdin
	defer func() { os.Stdin = oldStdin }()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdin = r
	go func() {
		_, err := w.Write([]byte("n\n"))
		require.NoError(t, err)
		err = w.Close()
		require.NoError(t, err)
	}()

	err = cmd.Execute()
	require.NoError(t, err)

	content, err := os.ReadFile(".diverge.yaml")
	require.NoError(t, err)
	assert.Equal(t, "existing content", string(content))
}
