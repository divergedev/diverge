package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func formatAge(t time.Time) string {
	if t.IsZero() {
		return "<unknown>"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func newListCmd(app *App) *cobra.Command {
	var (
		allNamespaces bool
		outputFormat  string
	)

	cmd := &cobra.Command{

		Use:   "list",
		Short: "List all environments in the cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			envClient, err := app.EnvironmentClient()
			if err != nil {
				return err
			}

			namespace := app.Namespace
			if allNamespaces {
				namespace = ""
			}

			envs, err := envClient.ListEnvironments(cmd.Context(), namespace)
			if err != nil {
				return fmt.Errorf("failed to list environments: %w", err)
			}

			switch outputFormat {
			case "json":
				b, err := json.MarshalIndent(envs, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal JSON: %w", err)
				}
				cmd.Println(string(b))
				return nil
			case "yaml":
				b, err := yaml.Marshal(envs)
				if err != nil {
					return fmt.Errorf("failed to marshal YAML: %w", err)
				}
				cmd.Println(string(b))
				return nil
			case "table", "":
				// Continue to table rendering below
			default:
				return fmt.Errorf("unsupported output format: %s (supported: table, json, yaml)", outputFormat)
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tPHASE\tAGE\tURL\tMR\tSERVICES")

			for _, env := range envs {
				phase := string(env.Status.Phase)
				if !app.NoColor {
					switch env.Status.Phase {
					case divergeiov1alpha1.PhaseRunning:
						phase = color.GreenString(phase)
					case divergeiov1alpha1.PhaseDeploying:
						phase = color.YellowString(phase)
					case divergeiov1alpha1.PhaseFailed:
						phase = color.RedString(phase)
					}
				}

				age := formatAge(env.CreationTimestamp.Time)

				services := fmt.Sprintf("%d", len(env.Status.Services))

				mr := fmt.Sprintf("%d", env.Spec.Source.MR)

				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					env.Name,
					phase,
					age,
					env.Status.URL,
					mr,
					services,
				)
			}
			_ = w.Flush()
			return nil
		},
	}
	cmd.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "list environments across all namespaces")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "", "output format: table, json, yaml")

	return cmd
}
