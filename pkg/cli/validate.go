package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/xeipuuv/gojsonschema"
	"sigs.k8s.io/yaml"
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
