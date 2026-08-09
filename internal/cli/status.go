package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func newStatusCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{

		Use:   "status <name>",
		Short: "Detailed status of a specific environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.KubeClient()
			if err != nil {
				return err
			}

			name := args[0]
			var env divergeiov1alpha1.Environment
			if err := c.Get(cmd.Context(), client.ObjectKey{Name: name, Namespace: app.Namespace}, &env); err != nil {
				return fmt.Errorf("failed to get environment %s: %w", name, err)
			}

			cmd.Printf("Name:      %s\n", env.Name)
			cmd.Printf("Namespace: %s\n", env.Namespace)
			cmd.Printf("Phase:     %s\n", env.Status.Phase)
			cmd.Printf("URL:       %s\n", env.Status.URL)
			if env.Status.CreatedAt != nil {
				cmd.Printf("Created:   %s\n", env.Status.CreatedAt.String())
			}
			if env.Status.ExpiresAt != nil {
				cmd.Printf("Expires:   %s\n", env.Status.ExpiresAt.String())
			}

			cmd.Println("\nConditions:")
			for _, cond := range env.Status.Conditions {
				cmd.Printf("  %s: %s (%s) - %s\n", cond.Type, cond.Status, cond.Reason, cond.Message)
			}

			cmd.Println("\nServices:")
			for _, svc := range env.Status.Services {
				cmd.Printf("  - %s\n", svc)
			}

			// Routing info
			cmd.Println("\nRouting:")
			cmd.Printf("  Mode: %s\n", env.Spec.Routing.Mode)
			if env.Spec.Routing.HeaderKey != "" {
				cmd.Printf("  Header: %s: %s\n", env.Spec.Routing.HeaderKey, env.Spec.Routing.HeaderValue)
			}

			return nil
		},
	}

	return cmd
}
