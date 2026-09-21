package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newTaskPauseCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pause <name>",
		Short: "Pause execution of an AgentTask",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskSetSuspended(cmd.Context(), app, args[0], true)
		},
	}
	return cmd
}

func newTaskResumeCmd(app *App) *cobra.Command {
	var (
		budgetUSD string
		tokens    int64
	)

	cmd := &cobra.Command{
		Use:   "resume <name>",
		Short: "Resume execution of a paused AgentTask",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskResume(cmd.Context(), app, args[0], budgetUSD, tokens)
		},
	}

	cmd.Flags().StringVar(&budgetUSD, "budget", "", "bump compute/token spend limit in USD (e.g. 10.00)")
	cmd.Flags().Int64Var(&tokens, "tokens", 0, "bump token spend limit (e.g. 1000000)")
	return cmd
}

func runTaskResume(ctx context.Context, app *App, name, budgetUSD string, tokens int64) error {
	if err := app.ResolveNamespace(); err != nil {
		return err
	}

	c, _, err := app.KubeClient()
	if err != nil {
		return fmt.Errorf("create kube client: %w", err)
	}

	taskClient := NewKubeTaskClient(c)
	if err := taskClient.Resume(ctx, app.Namespace, name, budgetUSD, tokens); err != nil {
		return err
	}

	var bumped []string
	if budgetUSD != "" {
		bumped = append(bumped, fmt.Sprintf("budget: $%s", budgetUSD))
	}
	if tokens > 0 {
		bumped = append(bumped, fmt.Sprintf("tokens: %d", tokens))
	}

	if len(bumped) > 0 {
		_, _ = fmt.Fprintf(os.Stdout, "AgentTask %q has been resumed with %s.\n", name, strings.Join(bumped, ", "))
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "AgentTask %q has been resumed.\n", name)
	}
	return nil
}

func runTaskSetSuspended(ctx context.Context, app *App, name string, suspended bool) error {
	if err := app.ResolveNamespace(); err != nil {
		return err
	}

	c, _, err := app.KubeClient()
	if err != nil {
		return fmt.Errorf("create kube client: %w", err)
	}

	taskClient := NewKubeTaskClient(c)
	if suspended {
		if err := taskClient.Pause(ctx, app.Namespace, name); err != nil {
			return err
		}
	} else {
		if err := taskClient.Resume(ctx, app.Namespace, name, "", 0); err != nil {
			return err
		}
	}

	state := "resumed"
	if suspended {
		state = "paused"
	}
	_, _ = fmt.Fprintf(os.Stdout, "AgentTask %q has been %s.\n", name, state)
	return nil
}
