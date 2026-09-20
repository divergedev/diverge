package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func newTaskCreateCmd(app *App) *cobra.Command {
	var (
		repoURL      string
		baseBranch   string
		poolRef      string
		templateRef  string
		budgetUSD    string
		maxIter      int32
		capabilities []string
		dryRun       bool
		output       string
		name         string
	)

	cmd := &cobra.Command{
		Use:   "create [objective]",
		Short: "Submit a new autonomous agent development task",
		Long: `Submit an autonomous agent development task with a natural language specification.
Diverge provisions an isolated sandbox, clones the repo, tests changes in on-demand preview
environments, and opens a draft PR.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			objective := strings.Join(args, " ")
			return runTaskCreate(cmd.Context(), app, objective, name, repoURL, baseBranch, poolRef, templateRef, budgetUSD, maxIter, capabilities, dryRun, output)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "explicit name for the task (auto-generated if empty)")
	cmd.Flags().StringVar(&repoURL, "repo", "", "Git repository clone URL (required)")
	cmd.Flags().StringVar(&baseBranch, "base-branch", "main", "base branch for work and PR")
	cmd.Flags().StringVar(&poolRef, "pool", "", "SandboxWarmPool name")
	cmd.Flags().StringVar(&templateRef, "template", "", "SandboxTemplate name")
	cmd.Flags().StringVar(&budgetUSD, "budget", "5.00", "compute/token spend limit in USD")
	cmd.Flags().Int32Var(&maxIter, "iterations", 5, "maximum autonomous build-test-review loops")
	cmd.Flags().StringSliceVar(&capabilities, "capabilities", []string{"fast", "smart"}, "model capability tiers")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print AgentTask specification without creating")
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format (text, yaml, json)")

	_ = cmd.MarkFlagRequired("repo")

	return cmd
}

func runTaskCreate(ctx context.Context, app *App, objective, name, repoURL, baseBranch, poolRef, templateRef, budgetUSD string, maxIter int32, capabilities []string, dryRun bool, output string) error {
	if err := app.ResolveNamespace(); err != nil {
		return err
	}

	taskName := name
	if taskName == "" {
		taskName = fmt.Sprintf("task-%d", time.Now().Unix())
	}

	task := &v1alpha1.AgentTask{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       "AgentTask",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskName,
			Namespace: app.Namespace,
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: objective,
			Repository: v1alpha1.AgentTaskRepository{
				URL:        repoURL,
				BaseBranch: baseBranch,
			},
			Sandbox: v1alpha1.AgentTaskSandbox{
				PoolRef:     poolRef,
				TemplateRef: templateRef,
			},
			BudgetUSD:     budgetUSD,
			MaxIterations: maxIter,
			Capabilities:  capabilities,
			DraftPR:       true,
		},
	}

	if dryRun {
		switch output {
		case "json":
			data, err := json.MarshalIndent(task, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
		default:
			data, err := yaml.Marshal(task)
			if err != nil {
				return err
			}
			fmt.Print(string(data))
		}
		return nil
	}

	c, _, err := app.KubeClient()
	if err != nil {
		return fmt.Errorf("create kube client: %w", err)
	}

	if err := c.Create(ctx, task); err != nil {
		return fmt.Errorf("create AgentTask %s: %w", taskName, err)
	}

	switch output {
	case "json":
		data, _ := json.MarshalIndent(task, "", "  ")
		fmt.Println(string(data))
	case "yaml":
		data, _ := yaml.Marshal(task)
		fmt.Print(string(data))
	default:
		_, _ = fmt.Fprintf(os.Stdout, "AgentTask %q submitted successfully in namespace %q.\n", taskName, app.Namespace)
		_, _ = fmt.Fprintf(os.Stdout, "Objective: %s\n", objective)
		_, _ = fmt.Fprintf(os.Stdout, "Run 'diverge task status %s' to monitor execution.\n", taskName)
	}

	return nil
}
