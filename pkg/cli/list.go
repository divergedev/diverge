package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

var (
	allNamespaces bool
	outputFormat  string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all environments in the cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := getKubeClient()
		if err != nil {
			return err
		}

		var envList divergeiov1alpha1.EnvironmentList
		listOpts := []client.ListOption{}

		if !allNamespaces {
			listOpts = append(listOpts, client.InNamespace(namespace))
		}

		if err := c.List(cmd.Context(), &envList, listOpts...); err != nil {
			return fmt.Errorf("failed to list environments: %w", err)
		}

		switch outputFormat {
		case "json":
			b, err := json.MarshalIndent(envList.Items, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal JSON: %w", err)
			}
			fmt.Println(string(b))
			return nil
		case "yaml":
			b, err := yaml.Marshal(envList.Items)
			if err != nil {
				return fmt.Errorf("failed to marshal YAML: %w", err)
			}
			fmt.Println(string(b))
			return nil
		case "table", "":
			// Continue to table rendering below
		default:
			return fmt.Errorf("unsupported output format: %s (supported: table, json, yaml)", outputFormat)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		_, _ = fmt.Fprintln(w, "NAME\tPHASE\tAGE\tURL\tMR\tSERVICES")

		for _, env := range envList.Items {
			phase := string(env.Status.Phase)
			if !noColor {
				switch env.Status.Phase {
				case divergeiov1alpha1.PhaseRunning:
					phase = color.GreenString(phase)
				case divergeiov1alpha1.PhaseDeploying:
					phase = color.YellowString(phase)
				case divergeiov1alpha1.PhaseFailed:
					phase = color.RedString(phase)
				}
			}

			age := ""
			if env.CreationTimestamp.Unix() > 0 {
				age = formatAge(env.CreationTimestamp.Time)
			}

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

func init() {
	listCmd.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "list environments across all namespaces")
	listCmd.Flags().StringVarP(&outputFormat, "output", "o", "", "output format: json, yaml, wide")
	rootCmd.AddCommand(listCmd)
}
