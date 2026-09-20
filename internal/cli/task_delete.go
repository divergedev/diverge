package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func newTaskDeleteCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an AgentTask and trigger sandbox teardown",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskDelete(cmd.Context(), app, args[0])
		},
	}
	return cmd
}

func runTaskDelete(ctx context.Context, app *App, name string) error {
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

	if err := c.Delete(ctx, task); err != nil {
		return fmt.Errorf("delete AgentTask %s: %w", name, err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "AgentTask %q in namespace %q marked for deletion.\n", name, app.Namespace)
	return nil
}
