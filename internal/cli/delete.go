package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

var deleteForce bool

func newDeleteCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{

		Use:   "delete <name>",
		Short: "Delete an environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.KubeClient()
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

			env := &divergeiov1alpha1.Environment{}
			env.Name = name
			env.Namespace = app.Namespace

			if err := c.Delete(cmd.Context(), env); err != nil {
				return fmt.Errorf("failed to delete environment %s: %w", name, err)
			}

			cmd.Printf("Environment %s deleted\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&deleteForce, "force", false, "force delete without confirmation")

	return cmd
}
