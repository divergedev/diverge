package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/divergedev/diverge/internal/async"
	"github.com/divergedev/diverge/internal/deployer"
	"github.com/divergedev/diverge/internal/notifier"
	"github.com/divergedev/diverge/internal/routing"
	divtesting "github.com/divergedev/diverge/internal/testing"
	pkgdb "github.com/divergedev/diverge/pkg/database"
)

// ProviderInfo contains details about a single registered provider.
type ProviderInfo struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func newProvidersCmd(app *App) *cobra.Command {
	providersCmd := &cobra.Command{
		Use:     "providers",
		Aliases: []string{"plugins", "plugin", "provider"},
		Short:   "Manage providers",
	}

	var outputFormat string

	listCmd := &cobra.Command{
		Use:               "list",
		Short:             "List all registered providers",
		DisableAutoGenTag: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var allProviders []ProviderInfo

			// Helper to add providers from a registry description map
			addRegistry := func(kind string, desc map[string]string) {
				for name, description := range desc {
					allProviders = append(allProviders, ProviderInfo{
						Kind:        kind,
						Name:        name,
						Description: description,
					})
				}
			}

			// Gather all providers
			addRegistry("router", routing.Providers.Describe())
			addRegistry("deployer", deployer.Providers.Describe())
			addRegistry("database", pkgdb.Providers.Describe())
			addRegistry("notifier", notifier.Providers.Describe())
			addRegistry("status-reporter", notifier.StatusProviders.Describe())
			addRegistry("previewgroup-notifier", notifier.GroupProviders.Describe())
			addRegistry("test-runner", divtesting.Providers.Describe())
			addRegistry("async-provisioner", async.Providers.Describe())

			// Sort for deterministic output
			sort.Slice(allProviders, func(i, j int) bool {
				if allProviders[i].Kind == allProviders[j].Kind {
					return allProviders[i].Name < allProviders[j].Name
				}
				return allProviders[i].Kind < allProviders[j].Kind
			})

			switch outputFormat {
			case "json":
				b, err := json.MarshalIndent(allProviders, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal JSON: %w", err)
				}
				cmd.Println(string(b))
				return nil
			case "yaml":
				b, err := yaml.Marshal(allProviders)
				if err != nil {
					return fmt.Errorf("failed to marshal YAML: %w", err)
				}
				cmd.Println(string(b))
				return nil
			case "table", "":
				// Fallthrough to table
			default:
				return fmt.Errorf("unsupported output format: %s (supported: table, json, yaml)", outputFormat)
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)

			var currentKind string
			for _, provider := range allProviders {
				if provider.Kind != currentKind {
					if currentKind != "" {
						if _, err := fmt.Fprintln(w); err != nil {
							return err
						}
					}
					if _, err := fmt.Fprintf(w, "--- %s ---\n", provider.Kind); err != nil {
						return err
					}
					if _, err := fmt.Fprintln(w, "NAME\tDESCRIPTION"); err != nil {
						return err
					}
					currentKind = provider.Kind
				}
				if _, err := fmt.Fprintf(w, "%s\t%s\n", provider.Name, provider.Description); err != nil {
					return err
				}
			}
			return w.Flush()
		},
	}

	listCmd.Flags().StringVarP(&outputFormat, "output", "o", "", "output format: table, json, yaml")

	providersCmd.AddCommand(listCmd)
	return providersCmd
}
