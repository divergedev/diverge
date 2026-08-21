package cli

import (
	"context"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDevCmd_DevspaceFlag_CreatesFile(t *testing.T) {
	// Setup a temporary directory for the test
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(originalWd) }()

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	err := runDev(nil, "", 0, "", true, "", nil, cmd)
	require.NoError(t, err)

	_, err = os.Stat("devspace.yaml")
	assert.NoError(t, err, "devspace.yaml should have been created")
}

func TestDevCmd_DevspaceFlag_SkipsWhenExists(t *testing.T) {
	// Setup a temporary directory for the test
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(originalWd) }()

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	// Create an existing file
	existingContent := []byte("existing content")
	err := os.WriteFile("devspace.yaml", existingContent, 0644)
	require.NoError(t, err)

	err = runDev(nil, "", 0, "", true, "", nil, cmd)
	require.NoError(t, err)

	content, err := os.ReadFile("devspace.yaml")
	require.NoError(t, err)
	assert.Equal(t, existingContent, content, "devspace.yaml should not be overwritten")
}

func TestDevCmd_DevspaceFlag_InjectsServiceName(t *testing.T) {
	// Setup a temporary directory for the test
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(originalWd) }()

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	// Use custom service name
	err := runDev(nil, "custom-app", 0, "", true, "", nil, cmd)
	require.NoError(t, err)

	content, err := os.ReadFile("devspace.yaml")
	require.NoError(t, err)

	assert.Contains(t, string(content), "DIVERGE_SERVICE: ${DIVERGE_SERVICE:-custom-app}")
}

func TestDevCmd_DevspaceFlag_ValidYaml(t *testing.T) {
	// Setup a temporary directory for the test
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(originalWd) }()

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	err := runDev(nil, "test-app", 0, "", true, "", nil, cmd)
	require.NoError(t, err)

	content, err := os.ReadFile("devspace.yaml")
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = yaml.Unmarshal(content, &parsed)
	assert.NoError(t, err, "devspace.yaml should be valid YAML")
	assert.Equal(t, "v2beta1", parsed["version"])
	assert.Equal(t, "diverge-dev", parsed["name"])
}
