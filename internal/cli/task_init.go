package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var defaultAgentConfigContent = strings.Join([]string{
	"# Diverge Agent Configuration",
	"# Defines permissions, model restrictions, and sandbox parameters for autonomous agents.",
	"version: v1",
	"agent:",
	"  # Allowed LLM models for this repository",
	"  allowedModels:",
	"    - claude-3-5-sonnet",
	"    - gpt-4o",
	"    - gemini-2.5-pro",
	"",
	"  # Allowed tools and commands within the sandbox",
	"  allowedTools:",
	"    - git",
	"    - go",
	"    - npm",
	"    - pytest",
	"    - diverge_*",
	"",
	"  # Default budget spend limits (USD)",
	"  budgetUSD: \"5.00\"",
	"",
	"  # Maximum autonomous build-test-review loops",
	"  maxIterations: 5",
	"",
	"  # Base branch for agent work and draft PRs",
	"  baseBranch: main",
	"",
	"  # Sandbox execution configuration",
	"  sandbox:",
	"    provider: agent-sandbox",
	"    timeoutSeconds: 3600",
	"",
}, "\n")

// AgentRepoConfig models the repository-level agent configuration file (.diverge/agent.yaml).
type AgentRepoConfig struct {
	Version string            `yaml:"version" json:"version"`
	Agent   AgentRepoSettings `yaml:"agent" json:"agent"`
}

// AgentRepoSettings contains configurable parameters for tasks targeting the repository.
type AgentRepoSettings struct {
	AllowedModels []string `yaml:"allowedModels,omitempty" json:"allowedModels,omitempty"`
	AllowedTools  []string `yaml:"allowedTools,omitempty" json:"allowedTools,omitempty"`
	BudgetUSD     string   `yaml:"budgetUSD,omitempty" json:"budgetUSD,omitempty"`
	MaxIterations int32    `yaml:"maxIterations,omitempty" json:"maxIterations,omitempty"`
	BaseBranch    string   `yaml:"baseBranch,omitempty" json:"baseBranch,omitempty"`
	Sandbox       struct {
		Provider       string `yaml:"provider,omitempty" json:"provider,omitempty"`
		PoolRef        string `yaml:"poolRef,omitempty" json:"poolRef,omitempty"`
		TemplateRef    string `yaml:"templateRef,omitempty" json:"templateRef,omitempty"`
		TimeoutSeconds int32  `yaml:"timeoutSeconds,omitempty" json:"timeoutSeconds,omitempty"`
	} `yaml:"sandbox,omitempty" json:"sandbox,omitempty"`
}

func newTaskInitCmd(_ *App) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize .diverge/agent.yaml configuration for the current repository",
		Long: `Scaffolds a default .diverge/agent.yaml file defining allowed models, tools,
budget bounds, and sandbox parameters for autonomous agent tasks.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskInit(force)
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite existing .diverge/agent.yaml")
	return cmd
}

func runTaskInit(force bool) error {
	dir := ".diverge"
	cfgPath := filepath.Join(dir, "agent.yaml")

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create %s directory: %w", dir, err)
	}

	if _, err := os.Stat(cfgPath); err == nil && !force {
		return fmt.Errorf("%s already exists; use --force to overwrite", cfgPath)
	}

	if err := os.WriteFile(cfgPath, []byte(defaultAgentConfigContent), 0644); err != nil {
		return fmt.Errorf("write %s: %w", cfgPath, err)
	}

	fmt.Printf("Initialized Diverge agent configuration at %s\n", cfgPath)
	return nil
}

// LoadAgentRepoConfig attempts to read and parse .diverge/agent.yaml from the specified directory or working dir.
func LoadAgentRepoConfig(rootDir string) (*AgentRepoConfig, error) {
	path := filepath.Join(rootDir, ".diverge", "agent.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg AgentRepoConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}
