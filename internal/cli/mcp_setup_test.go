package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPSetupDefaultAllClients(t *testing.T) {
	tmpDir := t.TempDir()
	app := &App{}
	rootCmd := NewRootCmd(app)

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"mcp", "setup", "--dir", tmpDir, "--server-url", "http://localhost:9090"})

	err := rootCmd.Execute()
	require.NoError(t, err)

	cursorPath := filepath.Join(tmpDir, ".cursor", "mcp.json")
	claudePath := filepath.Join(tmpDir, ".mcp.json")
	assert.FileExists(t, cursorPath)
	assert.FileExists(t, claudePath)

	for _, path := range []string{cursorPath, claudePath} {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)

		var parsed map[string]interface{}
		require.NoError(t, json.Unmarshal(raw, &parsed))

		servers, ok := parsed["mcpServers"].(map[string]interface{})
		require.True(t, ok)

		divergeEntry, ok := servers["diverge"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "diverge", divergeEntry["command"])
		assert.Equal(t, []interface{}{"mcp", "--server-url", "http://localhost:9090"}, divergeEntry["args"])
	}
}

func TestMCPSetupPreservesExistingServers(t *testing.T) {
	tmpDir := t.TempDir()
	cursorDir := filepath.Join(tmpDir, ".cursor")
	require.NoError(t, os.MkdirAll(cursorDir, 0o755))

	cursorPath := filepath.Join(cursorDir, "mcp.json")
	existingContent := `{
	"customTopLevel": "keep-me",
	"mcpServers": {
		"existing-server": {
			"command": "npx",
			"args": ["-y", "@modelcontextprotocol/server-github"]
		}
	}
}
`
	require.NoError(t, os.WriteFile(cursorPath, []byte(existingContent), 0o644))

	app := &App{}
	rootCmd := NewRootCmd(app)
	rootCmd.SetArgs([]string{"mcp", "setup", "--client", "cursor", "--dir", tmpDir})
	require.NoError(t, rootCmd.Execute())

	raw, err := os.ReadFile(cursorPath)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &parsed))
	assert.Equal(t, "keep-me", parsed["customTopLevel"])

	servers, ok := parsed["mcpServers"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, servers, "existing-server")
	assert.Contains(t, servers, "diverge")
}

func TestMCPSetupWindsurfAndDestructiveAndInvalidClient(t *testing.T) {
	tmpDir := t.TempDir()
	app := &App{}
	rootCmd := NewRootCmd(app)
	rootCmd.SetArgs([]string{
		"mcp", "setup",
		"--client", "windsurf",
		"--allow-destructive",
		"--dir", tmpDir,
	})
	require.NoError(t, rootCmd.Execute())

	windsurfPath := filepath.Join(tmpDir, ".codeium", "windsurf", "mcp_config.json")
	assert.FileExists(t, windsurfPath)

	raw, err := os.ReadFile(windsurfPath)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &parsed))
	servers := parsed["mcpServers"].(map[string]interface{})
	divergeEntry := servers["diverge"].(map[string]interface{})
	assert.Equal(t, []interface{}{"mcp", "--server-url", "http://localhost:8080", "--allow-destructive"}, divergeEntry["args"])

	// Invalid --client should return an error
	badCmd := NewRootCmd(&App{})
	badCmd.SetArgs([]string{"mcp", "setup", "--client", "unknown-editor", "--dir", tmpDir})
	err = badCmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported --client")
}
