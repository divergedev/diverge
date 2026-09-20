package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
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
	cmd := &cobra.Command{
		Use:   "resume <name>",
		Short: "Resume execution of a paused AgentTask",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskSetSuspended(cmd.Context(), app, args[0], false)
		},
	}
	return cmd
}

func runTaskSetSuspended(ctx context.Context, app *App, name string, suspended bool) error {
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

	task.Spec.Suspended = suspended
	if err := c.Update(ctx, task); err != nil {
		return fmt.Errorf("update AgentTask %s: %w", name, err)
	}

	state := "resumed"
	if suspended {
		state = "paused"
	}
	_, _ = fmt.Fprintf(os.Stdout, "AgentTask %q has been %s.\n", name, state)
	return nil
}
