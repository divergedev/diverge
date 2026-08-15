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

// PluginInfo contains details about a single registered plugin provider.
type PluginInfo struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func newPluginsCmd(app *App) *cobra.Command {
	pluginsCmd := &cobra.Command{
		Use:   "plugins",
		Short: "Manage plugins",
	}

	var outputFormat string

	listCmd := &cobra.Command{
		Use:               "list",
		Short:             "List all registered plugins",
		DisableAutoGenTag: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var allPlugins []PluginInfo

			// Helper to add plugins from a registry description map
			addRegistry := func(kind string, desc map[string]string) {
				for name, description := range desc {
					allPlugins = append(allPlugins, PluginInfo{
						Kind:        kind,
						Name:        name,
						Description: description,
					})
				}
			}

			// Gather all plugins
			addRegistry("router", routing.Providers.Describe())
			addRegistry("deployer", deployer.Providers.Describe())
			addRegistry("database", pkgdb.Providers.Describe())
			addRegistry("notifier", notifier.Providers.Describe())
			addRegistry("status-reporter", notifier.StatusProviders.Describe())
			addRegistry("previewgroup-notifier", notifier.GroupProviders.Describe())
			addRegistry("test-runner", divtesting.Providers.Describe())
			addRegistry("async-provisioner", async.Providers.Describe())

			// Sort for deterministic output
			sort.Slice(allPlugins, func(i, j int) bool {
				if allPlugins[i].Kind == allPlugins[j].Kind {
					return allPlugins[i].Name < allPlugins[j].Name
				}
				return allPlugins[i].Kind < allPlugins[j].Kind
			})

			switch outputFormat {
			case "json":
				b, err := json.MarshalIndent(allPlugins, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal JSON: %w", err)
				}
				cmd.Println(string(b))
				return nil
			case "yaml":
				b, err := yaml.Marshal(allPlugins)
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
			_, _ = fmt.Fprintln(w, "PROVIDER KIND\tNAME\tDESCRIPTION")

			for _, plugin := range allPlugins {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", plugin.Kind, plugin.Name, plugin.Description)
			}
			_ = w.Flush()
			return nil
		},
	}

	listCmd.Flags().StringVarP(&outputFormat, "output", "o", "", "output format: table, json, yaml")

	pluginsCmd.AddCommand(listCmd)
	return pluginsCmd
}
