package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func formatTTL(expiresAt *metav1.Time) string {
	if expiresAt == nil {
		return "-"
	}
	d := time.Until(expiresAt.Time)
	if d <= 0 {
		return "expired"
	}
	hours := int(d.Hours())
	if hours >= 24 {
		return fmt.Sprintf("%dh left", hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh left", hours)
	}
	mins := int(d.Minutes())
	return fmt.Sprintf("%dm left", mins)
}

func colorStatus(status string, noColor bool) string {
	if noColor {
		return status
	}
	s := strings.ToLower(status)
	if s == "ready" || s == "running" {
		return color.GreenString(status)
	}
	if s == "deploying" || s == "pending" {
		return color.YellowString(status)
	}
	if s == "failed" || s == "degraded" {
		return color.RedString(status)
	}
	return status
}

func colorTTL(ttl string, noColor bool) string {
	if noColor {
		return ttl
	}
	if ttl == "expired" {
		return color.RedString(ttl)
	}
	return ttl
}

func newStatusCmd(app *App) *cobra.Command {
	var namespaceFilter string
	var outputFormat string
	var wide bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show active preview environments and preview groups",
		Long:  "Display a summary of all active preview environments, preview groups, and their current state.",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.KubeClient()
			if err != nil {
				return err
			}

			listOpts := []client.ListOption{}
			if namespaceFilter != "" {
				listOpts = append(listOpts, client.InNamespace(namespaceFilter))
			}

			var envList divergeiov1alpha1.EnvironmentList
			if err := c.List(cmd.Context(), &envList, listOpts...); err != nil {
				return fmt.Errorf("failed to list environments: %w", err)
			}

			var pgList divergeiov1alpha1.PreviewGroupList
			if err := c.List(cmd.Context(), &pgList, listOpts...); err != nil {
				return fmt.Errorf("failed to list preview groups: %w", err)
			}

			switch outputFormat {
			case "json":
				data := map[string]interface{}{
					"environments":  envList.Items,
					"previewGroups": pgList.Items,
				}
				b, err := json.MarshalIndent(data, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal JSON: %w", err)
				}
				cmd.Println(string(b))
				return nil
			case "yaml":
				data := map[string]interface{}{
					"environments":  envList.Items,
					"previewGroups": pgList.Items,
				}
				b, err := yaml.Marshal(data)
				if err != nil {
					return fmt.Errorf("failed to marshal YAML: %w", err)
				}
				cmd.Println(string(b))
				return nil
			case "table", "":
				// table rendering
			default:
				return fmt.Errorf("unsupported output format: %s (supported: table, json, yaml)", outputFormat)
			}

			// Rendering Table
			cmd.Println("PREVIEW ENVIRONMENTS")
			if len(envList.Items) == 0 {
				cmd.Println("  No active environments found.")
			} else {
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
				if wide {
					_, _ = fmt.Fprintln(w, "  NAME\tNAMESPACE\tBRANCH\tPROVIDER\tSTATUS\tAGE\tTTL\tURL\tROUTING MODE\tHEADER VALUE\tSERVICES")
				} else {
					_, _ = fmt.Fprintln(w, "  NAME\tNAMESPACE\tBRANCH\tPROVIDER\tSTATUS\tAGE\tTTL\tURL")
				}

				for _, env := range envList.Items {
					phase := string(env.Status.Phase)
					if phase == "" {
						phase = "Unknown"
					}
					cPhase := colorStatus(phase, app.NoColor)

					age := formatAge(env.CreationTimestamp.Time)
					ttl := formatTTL(env.Status.ExpiresAt)
					cTtl := colorTTL(ttl, app.NoColor)

					url := env.Status.URL
					if url == "" {
						url = "-"
					}

					if wide {
						svcs := strings.Join(env.Status.Services, ",")
						if svcs == "" {
							svcs = "-"
						}
						_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
							env.Name, env.Namespace, env.Spec.Source.Branch, env.Spec.Source.Provider, cPhase, age, cTtl, url,
							env.Spec.Routing.Mode, env.Spec.Routing.HeaderValue, svcs)
					} else {
						_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
							env.Name, env.Namespace, env.Spec.Source.Branch, env.Spec.Source.Provider, cPhase, age, cTtl, url)
					}
				}
				_ = w.Flush()
			}
			cmd.Println()

			cmd.Println("PREVIEW GROUPS")
			servicesTotal := 0
			if len(pgList.Items) == 0 {
				cmd.Println("  No active preview groups found.")
			} else {
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
				if wide {
					_, _ = fmt.Fprintln(w, "  NAME\tSERVICES\tBRANCH\tPROVIDER\tSTATUS\tAGE\tROUTING MODE\tHEADER VALUE\tSERVICES LIST")
				} else {
					_, _ = fmt.Fprintln(w, "  NAME\tSERVICES\tBRANCH\tPROVIDER\tSTATUS\tAGE")
				}

				for _, pg := range pgList.Items {
					phase := string(pg.Status.Phase)
					if phase == "" {
						phase = "Unknown"
					}
					cPhase := colorStatus(phase, app.NoColor)
					age := formatAge(pg.CreationTimestamp.Time)

					numServices := len(pg.Spec.Services)
					servicesTotal += numServices

					if wide {
						var svcNames []string
						for _, s := range pg.Spec.Services {
							svcNames = append(svcNames, s.Name)
						}
						svcsStr := strings.Join(svcNames, ",")
						if svcsStr == "" {
							svcsStr = "-"
						}
						_, _ = fmt.Fprintf(w, "  %s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
							pg.Name, numServices, pg.Spec.Source.Branch, pg.Spec.Source.Provider, cPhase, age,
							pg.Spec.Routing.Mode, pg.Spec.Routing.HeaderValue, svcsStr)
					} else {
						_, _ = fmt.Fprintf(w, "  %s\t%d\t%s\t%s\t%s\t%s\n",
							pg.Name, numServices, pg.Spec.Source.Branch, pg.Spec.Source.Provider, cPhase, age)
					}
				}
				_ = w.Flush()
			}

			cmd.Printf("\nSUMMARY: %d environments, %d preview groups, %d services total\n", len(envList.Items), len(pgList.Items), servicesTotal)

			return nil
		},
	}
	cmd.Flags().StringVarP(&namespaceFilter, "namespace", "n", "", "Filter by namespace (default: all)")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "Output format: table, json, yaml")
	cmd.Flags().BoolVar(&wide, "wide", false, "Show additional columns")
	return cmd
}
