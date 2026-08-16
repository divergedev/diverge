package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var deleteForce bool

func newDeleteCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{

		Use:   "delete <name>",
		Short: "Delete an environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			envClient, err := app.EnvironmentClient()
			if err != nil {
				return err
			}

			name := args[0]

			if !deleteForce {
				var resp string
				cmd.Printf("Are you sure you want to delete environment %s? [y/N]: ", name)
				_, _ = fmt.Fscanln(cmd.InOrStdin(), &resp)
				if resp != "y" && resp != "Y" {
					cmd.Println("Cancelled")
					return nil
				}
			}

			if err := envClient.DeleteEnvironment(cmd.Context(), app.Namespace, name); err != nil {
				return fmt.Errorf("failed to delete environment %s: %w", name, err)
			}

			cmd.Printf("Environment %s deleted\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&deleteForce, "force", false, "force delete without confirmation")

	return cmd
}
