package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an environment from the current branch",
	RunE: func(cmd *cobra.Command, args []string) error {
		yamlPath := ".diverge.yaml"
		if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
			return fmt.Errorf("no %s found in current directory", yamlPath)
		}

		c, _, err := getKubeClient()
		if err != nil {
			return err
		}

		// Simple stub for creating the environment
		// In a real implementation we would parse .diverge.yaml, get the branch, repo, etc.

		envName := "preview-my-mr" // dummy

		env := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      envName,
				Namespace: namespace,
			},
			Spec: divergeiov1alpha1.EnvironmentSpec{
				Source: divergeiov1alpha1.EnvironmentSource{
					Provider: "gitlab",
					Project:  "my-project",
					MR:       1,
					Branch:   "feat/my-branch",
				},
				Deploy: divergeiov1alpha1.EnvironmentDeploy{
					Mode: "delta",
				},
				Routing: divergeiov1alpha1.EnvironmentRouting{
					Mode: "header",
				},
			},
		}

		if err := c.Create(cmd.Context(), env); err != nil {
			return fmt.Errorf("failed to create environment: %w", err)
		}

		fmt.Printf("Environment %s created\n", envName)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(createCmd)
}
