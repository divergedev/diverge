package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func newTaskListCmd(app *App) *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all AgentTasks in the namespace",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskList(cmd.Context(), app, output)
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "table", "output format (table, yaml, json)")
	return cmd
}

func runTaskList(ctx context.Context, app *App, output string) error {
	if err := app.ResolveNamespace(); err != nil {
		return err
	}

	c, _, err := app.KubeClient()
	if err != nil {
		return fmt.Errorf("create kube client: %w", err)
	}

	taskList := &v1alpha1.AgentTaskList{}
	if err := c.List(ctx, taskList, client.InNamespace(app.Namespace)); err != nil {
		return fmt.Errorf("list AgentTasks: %w", err)
	}

	switch output {
	case "json":
		data, err := json.MarshalIndent(taskList.Items, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	case "yaml":
		data, err := yaml.Marshal(taskList.Items)
		if err != nil {
			return err
		}
		fmt.Print(string(data))
	default:
		if len(taskList.Items) == 0 {
			_, _ = fmt.Fprintf(os.Stdout, "No AgentTasks found in namespace %q.\n", app.Namespace)
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		_, _ = fmt.Fprintln(w, "NAME\tPHASE\tSANDBOX POD\tITERATION\tPR LINK\tAGE")
		for _, t := range taskList.Items {
			pr := t.Status.PRURL
			if pr == "" {
				pr = "-"
			}
			pod := t.Status.SandboxPodName
			if pod == "" {
				pod = "-"
			}
			iter := fmt.Sprintf("%d/%d", t.Status.Iteration, t.Spec.MaxIterations)
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				t.Name,
				t.Status.Phase,
				pod,
				iter,
				pr,
				t.CreationTimestamp.Format("2006-01-02 15:04"),
			)
		}
		_ = w.Flush()
	}
	return nil
}
