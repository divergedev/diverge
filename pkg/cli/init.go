package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a .diverge.yaml in the current directory",
	RunE: func(cmd *cobra.Command, args []string) error {
		yamlPath := ".diverge.yaml"
		if _, err := os.Stat(yamlPath); err == nil {
			var resp string
			fmt.Printf("%s already exists. Overwrite? [y/N]: ", yamlPath)
			_, _ = fmt.Scanln(&resp)
			if resp != "y" && resp != "Y" {
				fmt.Println("Cancelled")
				return nil
			}
		}

		// Provide sensible defaults in the written file
		content := `version: "1"

# Define your services and how to detect changes for them
services:
  app:
    paths:
      - "src/**"
    image:
      repository: "registry.example.com/org/app"
      tag_template: "mr-{{.MR}}"

# Configure defaults for the environments
defaults:
  deploy:
    mode: delta # Only deploy changed services. Use 'full' to deploy all.
  routing:
    mode: header # Can be 'header', 'namespace', or 'subdomain'
    baseline_namespace: staging
  lifecycle:
    ttl: 72h
    cleanup_on_merge: true

# Define your environment types
environments:
  preview:
    trigger: label
    label: "diverge/deploy"
`
		if err := os.WriteFile(yamlPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", yamlPath, err)
		}

		fmt.Printf("Successfully created %s\n", yamlPath)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
