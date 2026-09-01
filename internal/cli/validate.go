package cli

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/xeipuuv/gojsonschema"
	"sigs.k8s.io/yaml"
)

//go:embed schema/diverge-config.schema.json
var schemaFS embed.FS

func newValidateCmd(app *App) *cobra.Command {
	var configPath string
	var outputFormat string

	cmd := &cobra.Command{

		Use:   "validate",
		Short: "Validate .diverge.yaml against the JSON Schema",
		RunE: func(cmd *cobra.Command, args []string) error {
			isRemote := strings.HasPrefix(configPath, "http://") || strings.HasPrefix(configPath, "https://")

			if !isRemote {
				if _, err := os.Stat(configPath); os.IsNotExist(err) {
					return fmt.Errorf("no %s found in current directory", configPath)
				}
			}

			schemaBytes, err := schemaFS.ReadFile("schema/diverge-config.schema.json")
			if err != nil {
				return fmt.Errorf("failed to read embedded schema: %w", err)
			}

			schemaLoader := gojsonschema.NewBytesLoader(schemaBytes)

			var yamlData []byte
			if isRemote {
				client := &http.Client{Timeout: 10 * time.Second}
				resp, err := client.Get(configPath)
				if err != nil {
					return fmt.Errorf("failed to fetch config from %s: %w", configPath, err)
				}
				defer func() { _ = resp.Body.Close() }()
				if resp.StatusCode != http.StatusOK {
					return fmt.Errorf("failed to fetch config from %s: status %d", configPath, resp.StatusCode)
				}
				yamlData, err = io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
				if err != nil {
					return fmt.Errorf("failed to read response body from %s: %w", configPath, err)
				}
			} else {
				yamlData, err = os.ReadFile(configPath)
				if err != nil {
					return fmt.Errorf("failed to read %s: %w", configPath, err)
				}
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
				if outputFormat == "json" {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), `{"valid": true, "errors": []}`)
				} else {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Config is valid.")
				}
				return nil
			}

			type ErrorObj struct {
				Path    string `json:"path"`
				Message string `json:"message"`
			}
			type ResultObj struct {
				Valid  bool       `json:"valid"`
				Errors []ErrorObj `json:"errors"`
			}

			var errorObjs []ErrorObj
			groupedErrs := make(map[string][]string)
			var orderedPaths []string

			for _, desc := range result.Errors() {
				path := desc.Field()
				if path == "(root)" {
					path = "$"
				} else {
					path = "$." + path
				}
				msg := desc.Description()

				errorObjs = append(errorObjs, ErrorObj{
					Path:    path,
					Message: msg,
				})

				if len(groupedErrs[path]) == 0 {
					orderedPaths = append(orderedPaths, path)
				}
				groupedErrs[path] = append(groupedErrs[path], msg)
			}

			if outputFormat == "json" {
				res := ResultObj{
					Valid:  false,
					Errors: errorObjs,
				}
				b, _ := json.Marshal(res)
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(b))
			} else {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Config is invalid. Errors:")
				for _, path := range orderedPaths {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "- %s\n", path)
					for _, msg := range groupedErrs[path] {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  - %s\n", msg)
					}
				}
			}

			cmd.SilenceUsage = true
			return fmt.Errorf("config validation failed with %d error(s)", len(result.Errors()))
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", ".diverge.yaml", "Path to config file")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "text", "Output format (text, json)")

	return cmd
}
