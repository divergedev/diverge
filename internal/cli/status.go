package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

var statusCmd = &cobra.Command{
	Use:   "status <name>",
	Short: "Detailed status of a specific environment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := getKubeClient()
		if err != nil {
			return err
		}

		name := args[0]
		var env divergeiov1alpha1.Environment
		if err := c.Get(cmd.Context(), client.ObjectKey{Name: name, Namespace: namespace}, &env); err != nil {
			return fmt.Errorf("failed to get environment %s: %w", name, err)
		}

		fmt.Printf("Name:      %s\n", env.Name)
		fmt.Printf("Namespace: %s\n", env.Namespace)
		fmt.Printf("Phase:     %s\n", env.Status.Phase)
		fmt.Printf("URL:       %s\n", env.Status.URL)
		if env.Status.CreatedAt != nil {
			fmt.Printf("Created:   %s\n", env.Status.CreatedAt.String())
		}
		if env.Status.ExpiresAt != nil {
			fmt.Printf("Expires:   %s\n", env.Status.ExpiresAt.String())
		}

		fmt.Println("\nConditions:")
		for _, cond := range env.Status.Conditions {
			fmt.Printf("  %s: %s (%s) - %s\n", cond.Type, cond.Status, cond.Reason, cond.Message)
		}

		fmt.Println("\nServices:")
		for _, svc := range env.Status.Services {
			fmt.Printf("  - %s\n", svc)
		}

		// Routing info
		fmt.Println("\nRouting:")
		fmt.Printf("  Mode: %s\n", env.Spec.Routing.Mode)
		if env.Spec.Routing.HeaderKey != "" {
			fmt.Printf("  Header: %s: %s\n", env.Spec.Routing.HeaderKey, env.Spec.Routing.HeaderValue)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
