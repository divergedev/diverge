package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/divergedev/diverge/internal/changeset"
	"github.com/divergedev/diverge/internal/config"
	"github.com/spf13/cobra"
)

type diffJSONOutput struct {
	BaseRef  string   `json:"baseRef"`
	Services []string `json:"services"`
	Count    int      `json:"count"`
}

func newDiffCmd(app *App) *cobra.Command {
	var (
		configFile string
		baseRef    string
		output     string
	)

	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Detect which services changed relative to a base branch",
		Long:  "Detect which services have changed by comparing the current branch\nagainst a base ref (default: main). Uses the 'paths' configuration in\n.diverge.yaml to map changed files to services.\n\nExamples:\n\tdiverge diff\t\t\t# compare against main\n\tdiverge diff --base origin/main\t# explicit base ref\n\tdiverge diff --output json\t# machine-readable output",
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true

			if output != "text" && output != "json" {
				return fmt.Errorf("unsupported output format %q: must be \"text\" or \"json\"", output)
			}

			cfg, err := config.Load(configFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			servicePaths := make(map[string][]string, len(cfg.Services))
			for svcName, svc := range cfg.Services {
				servicePaths[svcName] = svc.Paths
			}

			detector := &changeset.GitChangeDetector{}
			changed, err := detector.DetectChanges(cmd.Context(), "", "", baseRef, servicePaths)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()

			if output == "json" {
				if changed == nil {
					changed = []string{}
				}
				res := diffJSONOutput{
					BaseRef:  baseRef,
					Services: changed,
					Count:    len(changed),
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			// Text output
			if len(changed) == 0 {
				_, _ = fmt.Fprintf(out, "No services changed (compared to %s)\n", baseRef)
				return nil
			}

			_, _ = fmt.Fprintf(out, "Changed services (compared to %s):\n", baseRef)
			for _, svc := range changed {
				_, _ = fmt.Fprintf(out, "  • %s\n", svc)
			}
			_, _ = fmt.Fprintf(out, "\n%d service(s) affected\n", len(changed))

			// Show ingress impact if config has topology
			graph := buildGraphFromConfig(cfg)
			var impacted []string
			seen := make(map[string]bool)
			for _, svc := range changed {
				seen[svc] = true
			}
			for _, svc := range changed {
				for _, caller := range graph.UpstreamCallers(svc) {
					if !seen[caller] {
						seen[caller] = true
						impacted = append(impacted, caller)
					}
				}
			}
			if len(impacted) > 0 {
				_, _ = fmt.Fprintf(out, "\nUpstream services that may be affected:\n")
				for _, svc := range impacted {
					_, _ = fmt.Fprintf(out, "  ↑ %s\n", svc)
				}
			}

			// Show ingress paths for changed services
			entrypoints := graph.Entrypoints()
			if len(entrypoints) > 0 {
				_, _ = fmt.Fprintf(out, "\nIngress paths:\n")
				for _, svc := range changed {
					paths := graph.AllIngressPaths(svc)
					for _, p := range paths {
						_, _ = fmt.Fprintf(out, "  %s\n", strings.Join(p.Hops, " → "))
					}
					if len(paths) == 0 {
						_, _ = fmt.Fprintf(out, "  %s: no ingress path\n", svc)
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&configFile, "config", ".diverge.yaml", "path to diverge config file")
	cmd.Flags().StringVar(&baseRef, "base", "main", "base git ref to compare against")
	cmd.Flags().StringVar(&output, "output", "text", "output format (text, json)")

	return cmd
}
