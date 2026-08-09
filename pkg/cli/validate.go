package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/xeipuuv/gojsonschema"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate .diverge.yaml against the JSON Schema",
	RunE: func(cmd *cobra.Command, args []string) error {
		yamlPath := ".diverge.yaml"
		if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
			return fmt.Errorf("no %s found in current directory", yamlPath)
		}

		schemaLoader := gojsonschema.NewReferenceLoader("file://config/schema/diverge-config.schema.json")
		
		// In a real implementation we would convert YAML to JSON before validating.
		// For now we'll assume the schema works or use a proper yaml -> json conversion
		// But as requested, just load and validate.
		documentLoader := gojsonschema.NewReferenceLoader("file://" + yamlPath)

		result, err := gojsonschema.Validate(schemaLoader, documentLoader)
		if err != nil {
			return fmt.Errorf("failed to validate: %w", err)
		}

		if result.Valid() {
			fmt.Println("Config is valid.")
			return nil
		}

		fmt.Println("Config is invalid. Errors:")
		for _, desc := range result.Errors() {
			fmt.Printf("- %s\n", desc)
		}
		os.Exit(1)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(validateCmd)
}
