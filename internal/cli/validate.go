package cli

import (
	"embed"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/xeipuuv/gojsonschema"
	"sigs.k8s.io/yaml"
)

//go:embed schema/diverge-config.schema.json
var schemaFS embed.FS

func newValidateCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{

		Use:   "validate",
		Short: "Validate .diverge.yaml against the JSON Schema",
		RunE: func(cmd *cobra.Command, args []string) error {
			yamlPath := ".diverge.yaml"
			if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
				return fmt.Errorf("no %s found in current directory", yamlPath)
			}

			schemaBytes, err := schemaFS.ReadFile("schema/diverge-config.schema.json")
			if err != nil {
				return fmt.Errorf("failed to read embedded schema: %w", err)
			}

			schemaLoader := gojsonschema.NewBytesLoader(schemaBytes)

			yamlData, err := os.ReadFile(yamlPath)
			if err != nil {
				return fmt.Errorf("failed to read %s: %w", yamlPath, err)
			}

			jsonData, err := yaml.YAMLToJSON(yamlData)
			if err != nil {
				return fmt.Errorf("failed to convert YAML to JSON: %w", err)
			}

			documentLoader := gojsonschema.NewBytesLoader(jsonData)

			result, err := gojsonschema.Validate(schemaLoader, documentLoader)
			if err != nil {
				return fmt.Errorf("failed to validate: %w", err)
			}

			if result.Valid() {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Config is valid.")
				return nil
			}

			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Config is invalid. Errors:")
			for _, desc := range result.Errors() {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "- %s\n", desc)
			}
			cmd.SilenceUsage = true
			return fmt.Errorf("config validation failed with %d error(s)", len(result.Errors()))
		},
	}

	return cmd
}
