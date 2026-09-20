package cli

import (
	"github.com/spf13/cobra"
)

func newTaskCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage spec-driven autonomous agent tasks",
		Long: `Create and manage autonomous AI developer tasks in isolated K8s sandboxes.
Tasks develop code, test against on-demand Diverge preview environments, and submit draft PRs.`,
	}

	cmd.AddCommand(newTaskCreateCmd(app))
	cmd.AddCommand(newTaskStatusCmd(app))
	cmd.AddCommand(newTaskListCmd(app))
	cmd.AddCommand(newTaskLogsCmd(app))
	cmd.AddCommand(newTaskDeleteCmd(app))
	cmd.AddCommand(newTaskPauseCmd(app))
	cmd.AddCommand(newTaskResumeCmd(app))

	return cmd
}
