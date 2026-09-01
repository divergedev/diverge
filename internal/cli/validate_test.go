package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateValidConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".diverge.yaml")
	// editorconfig-checker-disable
	err := os.WriteFile(configPath, []byte(`version: "1"
services:
  api:
    paths: ["src/**"]
    image:
      repository: "registry.example.com/api"
`), 0644)
	// editorconfig-checker-enable
	require.NoError(t, err)

	// Change to temp dir so validate finds .diverge.yaml
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	app := &App{}
	cmd := newValidateCmd(app)
	var out bytes.Buffer
	cmd.SetOut(&out)

	err = cmd.RunE(cmd, nil)
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "Config is valid")
}

func TestValidateInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".diverge.yaml")
	err := os.WriteFile(configPath, []byte(`version: 999
`), 0644)
	require.NoError(t, err)

	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	app := &App{}
	cmd := newValidateCmd(app)
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)

	err = cmd.RunE(cmd, nil)
	// Should return error, NOT call os.Exit
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config validation failed")
	assert.Contains(t, errBuf.String(), "Config is invalid")
}

func TestValidateMissingConfig(t *testing.T) {
	dir := t.TempDir()

	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	app := &App{}
	cmd := newValidateCmd(app)

	err := cmd.RunE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no .diverge.yaml found")
}

func TestValidateWorksFromAnyDirectory(t *testing.T) {
	// This test verifies the schema is embedded (not loaded from relative path).
	// If the schema were loaded from file://config/schema/..., this would fail
	// when running from a temp directory.
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".diverge.yaml")
	err := os.WriteFile(configPath, []byte(`version: "1"
`), 0644)
	require.NoError(t, err)

	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	app := &App{}
	cmd := newValidateCmd(app)
	var out bytes.Buffer
	cmd.SetOut(&out)

	err = cmd.RunE(cmd, nil)
	assert.NoError(t, err, "validate should work from any directory with embedded schema")
}

func TestValidateRemoteConfig(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// editorconfig-checker-disable
		_, _ = w.Write([]byte(`version: "1"
services:
  api:
    paths: ["src/**"]
    image:
      repository: "registry.example.com/api"`))
		// editorconfig-checker-enable
	}))
	defer ts.Close()

	app := &App{}
	cmd := newValidateCmd(app)
	var out bytes.Buffer
	cmd.SetOut(&out)

	cmd.SetArgs([]string{"--config", ts.URL})
	err := cmd.Execute()
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "Config is valid")
}
