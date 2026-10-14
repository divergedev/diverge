package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newMCPSetupCmd(_ *App) *cobra.Command {
	var (
		clientTarget     string
		serverURL        string
		allowDestructive bool
		targetDir        string
	)

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Configure Diverge MCP server for Cursor, Claude Code, and Windsurf",
		Long: `Automatically generates or updates Model Context Protocol (MCP) configuration
files for AI coding assistants (Cursor, Claude Code, Windsurf) while preserving
any existing MCP servers already configured in your workspace.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(serverURL) == "" {
				serverURL = "http://localhost:8080"
			}
			if strings.TrimSpace(targetDir) == "" {
				targetDir = "."
			}

			args := []string{"mcp", "--server-url", serverURL}
			if allowDestructive {
				args = append(args, "--allow-destructive")
			}
			entry := map[string]interface{}{
				"command": "diverge",
				"args":    args,
			}

			var targets []string
			switch strings.ToLower(strings.TrimSpace(clientTarget)) {
			case "all", "":
				windsurfPath, err := resolveWindsurfConfigPath()
				if err != nil {
					return err
				}
				targets = []string{
					filepath.Join(targetDir, ".cursor", "mcp.json"),
					filepath.Join(targetDir, ".mcp.json"),
					windsurfPath,
				}
			case "cursor":
				targets = []string{
					filepath.Join(targetDir, ".cursor", "mcp.json"),
				}
			case "claude":
				targets = []string{
					filepath.Join(targetDir, ".mcp.json"),
				}
			case "windsurf":
				windsurfPath, err := resolveWindsurfConfigPath()
				if err != nil {
					return err
				}
				targets = []string{
					windsurfPath,
				}
			default:
				return fmt.Errorf("unsupported --client %q (supported: all, cursor, claude, windsurf)", clientTarget)
			}

			for _, path := range targets {
				if err := upsertMCPConfigFile(path, entry); err != nil {
					return err
				}
				cmd.Printf("Configured Diverge MCP server in %s\n", path)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&clientTarget, "client", "all", "Target AI client: all, cursor, claude, windsurf")
	cmd.Flags().StringVar(&serverURL, "server-url", "http://localhost:8080", "Diverge API server URL")
	cmd.Flags().BoolVar(&allowDestructive, "allow-destructive", false, "Include --allow-destructive in MCP args")
	cmd.Flags().StringVar(&targetDir, "dir", ".", "Target workspace directory")

	return cmd
}

func resolveWindsurfConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		return "", fmt.Errorf("resolve user home directory for Windsurf MCP config: %w", err)
	}
	return filepath.Join(homeDir, ".codeium", "windsurf", "mcp_config.json"), nil
}

func upsertMCPConfigFile(path string, serverEntry map[string]interface{}) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}

	fileMode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		fileMode = info.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("stat existing MCP config %s: %w", path, statErr)
	}

	root := make(map[string]interface{})
	if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse existing MCP config %s: %w", path, err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read existing MCP config %s: %w", path, err)
	}

	mcpServers, ok := root["mcpServers"].(map[string]interface{})
	if !ok || mcpServers == nil {
		mcpServers = make(map[string]interface{})
	}
	mcpServers["diverge"] = serverEntry
	root["mcpServers"] = mcpServers

	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal MCP config %s: %w", path, err)
	}
	encoded = append(encoded, '\n')

	tmpFile, err := os.CreateTemp(dir, ".mcp-config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp MCP config in %s: %w", dir, err)
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(encoded); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp MCP config %s: %w", tmpPath, err)
	}
	if err := tmpFile.Chmod(fileMode); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("chmod temp MCP config %s: %w", tmpPath, err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp MCP config %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace MCP config %s: %w", path, err)
	}
	cleanupTmp = false
	return nil
}
