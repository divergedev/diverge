package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/config"
	"github.com/divergedev/diverge/internal/git"
)

var (
	configPath string
	envName    string
	envType    string
	mrNumber   int
	labels     string
	dryRun     bool
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an environment from the current branch",
	Long: `Create a Diverge preview environment by reading .diverge.yaml
and detecting the current git context (branch, provider, project).

The environment name is generated deterministically from the MR/PR number
or branch name, unless overridden with --name.`,
	RunE: runCreate,
}

func runCreate(cmd *cobra.Command, _ []string) error {
	// Load config
	cfg, err := config.Load(configPath)
	if err != nil && !os.IsNotExist(err) {
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
	env := buildEnvironment(name, gitCtx, resolved, cfg)

	if dryRun {
		return printDryRun(env)
	}

	// Create via K8s client
	c, _, err := getKubeClient()
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	if err := c.Create(cmd.Context(), env); err != nil {
		return fmt.Errorf("failed to create environment: %w", err)
	}

	fmt.Printf("✅ Environment %q created in namespace %q\n", env.Name, env.Namespace)
	if resolved.Routing.Domain != "" {
		fmt.Printf("🌐 URL: https://%s.%s\n", name, resolved.Routing.Domain)
	}
	return nil
}

func generateEnvName(envType string, mr int, branch string) string {
	if mr > 0 {
		return fmt.Sprintf("%s-mr-%d", envType, mr)
	}
	slug := git.SlugifyBranch(branch)
	return fmt.Sprintf("%s-%s", envType, slug)
}

func buildEnvironment(name string, gitCtx *git.GitContext, resolved *config.ResolvedSettings, cfg *config.Config) *divergeiov1alpha1.Environment {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
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
				Mode: resolved.Deploy.Mode,
			},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				Mode:      resolved.Routing.Mode,
				HeaderKey: resolved.Routing.HeaderKey,
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
		if err == nil {
			env.Spec.Lifecycle.TTL = duration
		}
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
		var changedServices []string
		for svcName := range cfg.Services {
			changedServices = append(changedServices, svcName)
		}
		env.Spec.Deploy.ChangedServices = changedServices
	}

	// Set MR number in labels if available
	if mrNumber > 0 {
		env.Labels["diverge.dev/mr"] = strconv.Itoa(mrNumber)
	}

	return env
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

func init() {
	createCmd.Flags().StringVarP(&configPath, "config", "c", ".diverge.yaml", "path to config file")
	createCmd.Flags().StringVar(&envName, "name", "", "override environment name")
	createCmd.Flags().StringVarP(&envType, "env-type", "t", "preview", "environment type from config")
	createCmd.Flags().IntVar(&mrNumber, "mr", 0, "MR/PR number")
	createCmd.Flags().StringVarP(&labels, "labels", "l", "", "comma-separated MR labels for overrides")
	createCmd.Flags().BoolVar(&dryRun, "dry-run", false, "print Environment YAML without creating")
	rootCmd.AddCommand(createCmd)
}
