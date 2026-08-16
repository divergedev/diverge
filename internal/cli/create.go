package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/changeset"
	"github.com/divergedev/diverge/internal/config"
	"github.com/divergedev/diverge/internal/git"
)

func newCreateCmd(app *App) *cobra.Command {
	var (
		configPath string
		envName    string
		envType    string
		mrNumber   int
		labels     string
		dryRun     bool
	)

	cmd := &cobra.Command{

		Use:   "create",
		Short: "Create an environment from the current branch",
		Long: `Create a Diverge preview environment by reading .diverge.yaml
and detecting the current git context (branch, provider, project).

The environment name is generated deterministically from the MR/PR number
or branch name, unless overridden with --name.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCreate(cmd, args, app, configPath, envName, envType, mrNumber, labels, dryRun)
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", ".diverge.yaml", "path to config file")
	cmd.Flags().StringVar(&envName, "name", "", "override environment name")
	cmd.Flags().StringVarP(&envType, "env-type", "t", "preview", "environment type from config")
	cmd.Flags().IntVar(&mrNumber, "mr", 0, "MR/PR number")
	cmd.Flags().StringVarP(&labels, "labels", "l", "", "comma-separated MR labels for overrides")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print Environment YAML without creating")

	return cmd
}

func runCreate(cmd *cobra.Command, _ []string, app *App, configPath, envName, envType string, mrNumber int, labels string, dryRun bool) error {
	// Load config
	cfg, err := config.Load(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// If config file doesn't exist, use empty defaults
	if cfg == nil {
		cfg = &config.Config{Version: "1"}
	}

	// Detect git context
	gitCtx, err := git.Detect()
	if err != nil {
		return fmt.Errorf("failed to detect git context: %w", err)
	}

	// Parse labels
	var labelList []string
	if labels != "" {
		labelList = strings.Split(labels, ",")
		for i := range labelList {
			labelList[i] = strings.TrimSpace(labelList[i])
		}
	}

	// Resolve config
	resolved := cfg.Resolve(envType, labelList)

	// Generate environment name
	name := envName
	if name == "" {
		name = generateEnvName(envType, mrNumber, gitCtx.Branch)
	}

	// Build the Environment CR
	env, err := buildEnvironment(cmd.Context(), name, gitCtx, resolved, cfg, app, mrNumber)
	if err != nil {
		return fmt.Errorf("failed to build environment: %w", err)
	}

	if dryRun {
		return printDryRun(env)
	}

	envClient, err := app.EnvironmentClient()
	if err != nil {
		return fmt.Errorf("failed to get environment client: %w", err)
	}

	if _, err := envClient.CreateEnvironment(cmd.Context(), env); err != nil {
		return fmt.Errorf("failed to create environment: %w", err)
	}

	fmt.Printf("✅ Environment requested. Run 'diverge status %s' to monitor deployment progress.\n", env.Name)
	if resolved.Routing.Domain != "" {
		fmt.Printf("🌐 URL: https://%s.%s\n", name, resolved.Routing.Domain)
	}
	return nil
}

func generateEnvName(envType string, mr int, branch string) string {
	var name string
	if mr > 0 {
		name = fmt.Sprintf("%s-mr-%d", envType, mr)
	} else {
		slug := git.SlugifyBranch(branch)
		name = fmt.Sprintf("%s-%s", envType, slug)
	}
	// K8s label values must be ≤63 characters
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

func buildEnvironment(ctx context.Context, name string, gitCtx *git.GitContext, resolved *config.ResolvedSettings, cfg *config.Config, app *App, mrNumber int) (*divergeiov1alpha1.Environment, error) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: app.Namespace,
			Labels: map[string]string{
				"diverge.dev/environment": name,
				"diverge.dev/provider":    gitCtx.Provider,
			},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: gitCtx.Provider,
				Project:  gitCtx.Project,
				Branch:   gitCtx.Branch,
				MR:       mrNumber,
			},
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Mode:      resolved.Deploy.Mode,
				Namespace: resolved.Deploy.Namespace,
			},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				Mode:        resolved.Routing.Mode,
				HeaderKey:   resolved.Routing.HeaderKey,
				HeaderValue: name,
			},
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Mode:          resolved.Database.Mode,
				ConnectionRef: resolved.Database.ConnectionRef,
				SeedSource:    resolved.Database.SeedSource,
			},
		},
	}

	// Set TTL if configured
	if resolved.Lifecycle.TTL != "" {
		duration, err := parseDuration(resolved.Lifecycle.TTL)
		if err != nil {
			return nil, fmt.Errorf("invalid TTL %q: %w", resolved.Lifecycle.TTL, err)
		}
		env.Spec.Lifecycle.TTL = duration
	}

	if resolved.Lifecycle.CleanupOnMerge != nil {
		env.Spec.Lifecycle.CleanupOnMerge = *resolved.Lifecycle.CleanupOnMerge
	}

	// Set external URL if domain is configured
	if resolved.Routing.Domain != "" {
		env.Spec.Routing.ExternalURL = fmt.Sprintf("https://%s.%s", name, resolved.Routing.Domain)
	}

	// Detect changed services for delta mode
	if resolved.Deploy.Mode == "delta" && cfg != nil {
		servicePaths := make(map[string][]string, len(cfg.Services))
		for svcName, svc := range cfg.Services {
			servicePaths[svcName] = svc.Paths
		}
		detector := &changeset.GitChangeDetector{}
		changed, err := detector.DetectChanges(ctx, gitCtx.Project, gitCtx.Branch, "main", servicePaths)
		if err != nil {
			// Fall back to all services if detection fails
			for svcName := range cfg.Services {
				changed = append(changed, svcName)
			}
		}
		if len(changed) == 0 {
			// If detector returns empty (stub), include all services
			for svcName := range cfg.Services {
				changed = append(changed, svcName)
			}
		}
		env.Spec.Deploy.ChangedServices = changed
	}

	// Set MR number in labels if available
	if mrNumber > 0 {
		env.Labels["diverge.dev/mr"] = strconv.Itoa(mrNumber)
	}

	return env, nil
}

func parseDuration(s string) (*metav1.Duration, error) {
	if s == "" {
		return nil, fmt.Errorf("empty duration")
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil, err
	}
	return &metav1.Duration{Duration: d}, nil
}

func printDryRun(env *divergeiov1alpha1.Environment) error {
	data, err := yaml.Marshal(env)
	if err != nil {
		return fmt.Errorf("failed to marshal environment: %w", err)
	}
	fmt.Println("---")
	fmt.Printf("# dry-run: would create Environment %q in namespace %q\n", env.Name, env.Namespace)
	fmt.Print(string(data))
	return nil
}
