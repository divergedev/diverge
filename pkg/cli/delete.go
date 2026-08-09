package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

var deleteForce bool

var deleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an environment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := getKubeClient()
		if err != nil {
			return err
		}

		name := args[0]
		
		if !deleteForce {
			var resp string
			fmt.Printf("Are you sure you want to delete environment %s? [y/N]: ", name)
			fmt.Scanln(&resp)
			if resp != "y" && resp != "Y" {
				fmt.Println("Cancelled")
				return nil
			}
		}

		env := &divergeiov1alpha1.Environment{}
		env.Name = name
		env.Namespace = namespace

		if err := c.Delete(context.Background(), env); err != nil {
			return fmt.Errorf("failed to delete environment %s: %w", name, err)
		}

		fmt.Printf("Environment %s deleted\n", name)
		return nil
	},
}

func init() {
	deleteCmd.Flags().BoolVar(&deleteForce, "force", false, "force delete without confirmation")
	rootCmd.AddCommand(deleteCmd)
}
