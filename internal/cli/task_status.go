package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func newTaskStatusCmd(app *App) *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Display live status and progress of an AgentTask",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskStatus(cmd.Context(), app, args[0], output)
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format (text, yaml, json)")
	return cmd
}

func runTaskStatus(ctx context.Context, app *App, name, output string) error {
	if err := app.ResolveNamespace(); err != nil {
		return err
	}

	c, _, err := app.KubeClient()
	if err != nil {
		return fmt.Errorf("create kube client: %w", err)
	}

	task := &v1alpha1.AgentTask{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: app.Namespace, Name: name}, task); err != nil {
		return fmt.Errorf("get AgentTask %s: %w", name, err)
	}

	switch output {
	case "json":
		data, err := json.MarshalIndent(task, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	case "yaml":
		data, err := yaml.Marshal(task)
		if err != nil {
			return err
		}
		fmt.Print(string(data))
	default:
		_, _ = fmt.Fprintf(os.Stdout, "Task:         %s\n", task.Name)
		_, _ = fmt.Fprintf(os.Stdout, "Namespace:    %s\n", task.Namespace)
		_, _ = fmt.Fprintf(os.Stdout, "Phase:        %s\n", task.Status.Phase)
		_, _ = fmt.Fprintf(os.Stdout, "Objective:    %s\n", task.Spec.Objective)
		_, _ = fmt.Fprintf(os.Stdout, "Repository:   %s (base: %s)\n", task.Spec.Repository.URL, task.Spec.Repository.BaseBranch)
		if task.Status.SandboxPodName != "" {
			_, _ = fmt.Fprintf(os.Stdout, "Sandbox Pod:  %s (IP: %s)\n", task.Status.SandboxPodName, task.Status.SandboxIP)
		}
		if task.Status.Iteration > 0 {
			_, _ = fmt.Fprintf(os.Stdout, "Iteration:    %d / %d\n", task.Status.Iteration, task.Spec.MaxIterations)
		}
		if task.Status.CostUSD != "" {
			_, _ = fmt.Fprintf(os.Stdout, "Cost:         $%s / $%s\n", task.Status.CostUSD, task.Spec.BudgetUSD)
		}
		if task.Status.PRURL != "" {
			_, _ = fmt.Fprintf(os.Stdout, "PR Link:      %s\n", task.Status.PRURL)
		}
		if task.Status.Message != "" {
			_, _ = fmt.Fprintf(os.Stdout, "Status Msg:   %s\n", task.Status.Message)
		}
	}
	return nil
}
