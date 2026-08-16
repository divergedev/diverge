package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const configDir = ".config/diverge"

// Config represents the CLI configuration.
type Config struct {
	ActiveContext string             `json:"active_context"`
	Contexts      map[string]Context `json:"contexts"`
}

// Context represents a named server context.
type Context struct {
	ServerURL    string    `json:"server_url"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

// configPath returns ~/.config/diverge/config.json
func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, configDir, "config.json")
}

// LoadConfig loads the CLI config from disk.
func LoadConfig() (*Config, error) {
	path := configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Contexts: make(map[string]Context)}, nil
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Contexts == nil {
		c.Contexts = make(map[string]Context)
	}
	return &c, nil
}

func (c *Config) Save() error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// Atomic write: tmpfile + rename
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("writing temp config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming config: %w", err)
	}
	return nil
}

// ActiveServerURL returns the current server URL, or empty for direct K8s mode.
func (c *Config) ActiveServerURL() string {
	if c.ActiveContext == "" {
		return ""
	}
	if ctx, ok := c.Contexts[c.ActiveContext]; ok {
		return ctx.ServerURL
	}
	return ""
}

// ActiveToken returns the bearer token, refreshing if expired.
func (c *Config) ActiveToken() (string, error) {
	ctx, ok := c.Contexts[c.ActiveContext]
	if !ok {
		return "", fmt.Errorf("no active context")
	}
	// If token hasn't expired, return it
	if ctx.ExpiresAt.IsZero() || time.Now().Before(ctx.ExpiresAt.Add(-30*time.Second)) {
		return ctx.AccessToken, nil
	}
	// Token expired but we have a refresh token
	if ctx.RefreshToken != "" {
		// TODO: implement token refresh via OIDC token endpoint
		return "", fmt.Errorf("access token expired, please run 'diverge login' again")
	}
	return "", fmt.Errorf("access token expired and no refresh token available")
}

func saveCredentials(serverURL, accessToken, refreshToken string, expiresAt time.Time) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	// Use serverURL as the context name
	ctxName := serverURL
	cfg.Contexts[ctxName] = Context{
		ServerURL:    serverURL,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
	}
	cfg.ActiveContext = ctxName
	return cfg.Save()
}
