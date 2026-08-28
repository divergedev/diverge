package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/divergedev/diverge/internal/config"
	"github.com/divergedev/diverge/pkg/topology"
)

// newGraphCmd creates the `diverge graph` command group
func newGraphCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Manage and visualize service topology graphs",
	}

	cmd.AddCommand(newGraphShowCmd(app))
	cmd.AddCommand(newGraphValidateCmd(app))

	return cmd
}

func buildGraphFromConfig(cfg *config.Config) *topology.ServiceGraph {
	g := topology.NewServiceGraph()
	if cfg == nil {
		return g
	}

	for name, svc := range cfg.Services {
		g.AddNode(name)
		if svc.Entrypoint {
			g.AddEntrypoint(name)
		}
		for _, dep := range svc.DependsOn {
			g.AddEdge(topology.Edge{
				From:   name,
				To:     dep,
				Source: "static",
			})
		}
	}

	return g
}

func newGraphShowCmd(app *App) *cobra.Command {
	var configPath string
	var service string

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Display the discovered service topology",
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true

			cfg, err := config.Load(configPath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("config file %s not found", configPath)
				}
				return fmt.Errorf("failed to load config: %w", err)
			}

			g := buildGraphFromConfig(cfg)
			out := cmd.OutOrStdout()

			if service != "" {
				_, _ = fmt.Fprintf(out, "Ingress paths to %s:\n", service)
				paths := g.AllIngressPaths(service)
				if len(paths) == 0 {
					_, _ = fmt.Fprintf(out, "  (none)\n")
				} else {
					for _, p := range paths {
						_, _ = fmt.Fprintf(out, "  ")
						for i, hop := range p.Hops {
							if i > 0 {
								_, _ = fmt.Fprintf(out, " → ")
							}
							_, _ = fmt.Fprintf(out, "%s", hop)
						}
						_, _ = fmt.Fprintf(out, " (%d hops)\n", len(p.Hops)-1)
					}
				}

				_, _ = fmt.Fprintf(out, "\nUpstream callers:\n")
				up := g.UpstreamCallers(service)
				if len(up) == 0 {
					_, _ = fmt.Fprintf(out, "  (none)\n")
				} else {
					for _, u := range up {
						_, _ = fmt.Fprintf(out, "  %s\n", u)
					}
				}

				_, _ = fmt.Fprintf(out, "\nDownstream callees:\n")
				down := g.Neighbors(service)
				if len(down) == 0 {
					_, _ = fmt.Fprintf(out, "  (none)\n")
				} else {
					for _, d := range down {
						_, _ = fmt.Fprintf(out, "  %s\n", d)
					}
				}
				return nil
			}

			_, _ = fmt.Fprintf(out, "Service Graph (source: static)\n")
			eps := g.Entrypoints()
			for _, ep := range eps {
				_, _ = fmt.Fprintf(out, "  ● %s (entrypoint)\n", ep)
				printTree(out, g, ep, "    ", make(map[string]bool))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", ".diverge.yaml", "path to config file")
	cmd.Flags().StringVar(&service, "service", "", "show ingress paths for a specific service")

	return cmd
}

func printTree(out io.Writer, g *topology.ServiceGraph, node string, prefix string, visited map[string]bool) {
	if visited[node] {
		return
	}
	visited[node] = true

	neighbors := g.Neighbors(node)
	for i, neighbor := range neighbors {
		isLast := i == len(neighbors)-1

		_, _ = fmt.Fprintf(out, "%s", prefix)
		if isLast {
			_, _ = fmt.Fprintf(out, "└── → %s\n", neighbor)
			printTree(out, g, neighbor, prefix+"    ", visited)
		} else {
			_, _ = fmt.Fprintf(out, "├── → %s\n", neighbor)
			printTree(out, g, neighbor, prefix+"│   ", visited)
		}
	}
	visited[node] = false
}

func editDistance(s1, s2 string) int {
	len1, len2 := len(s1), len(s2)
	if len1 == 0 {
		return len2
	}
	if len2 == 0 {
		return len1
	}

	row := make([]int, len2+1)
	for i := 0; i <= len2; i++ {
		row[i] = i
	}

	for i := 1; i <= len1; i++ {
		prev := i
		for j := 1; j <= len2; j++ {
			var current int
			if s1[i-1] == s2[j-1] {
				current = row[j-1]
			} else {
				a := row[j-1]
				b := prev
				c := row[j]
				minVal := a
				if b < minVal {
					minVal = b
				}
				if c < minVal {
					minVal = c
				}
				current = 1 + minVal
			}
			row[j-1] = prev
			prev = current
		}
		row[len2] = prev
	}
	return row[len2]
}

func newGraphValidateCmd(app *App) *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "check for cycles and missing references",
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true

			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			g := buildGraphFromConfig(cfg)
			out := cmd.OutOrStdout()

			if err := g.Validate(); err != nil {
				_, _ = fmt.Fprintf(out, "✗ Error: %s\n", err.Error())
				return err
			}
			_, _ = fmt.Fprintf(out, "✓ No cycles detected\n")

			valid := true
			for name, svc := range cfg.Services {
				for _, dep := range svc.DependsOn {
					// Check for self-referential dependencies
					if dep == name {
						valid = false
						_, _ = fmt.Fprintf(out, "✗ Error: service '%s' depends on itself\n", name)
						continue
					}
					if _, exists := cfg.Services[dep]; !exists {
						valid = false
						bestMatch := ""
						bestDist := 999
						for existing := range cfg.Services {
							dist := editDistance(dep, existing)
							if dist < bestDist {
								bestDist = dist
								bestMatch = existing
							}
						}

						if bestDist <= 3 && bestMatch != "" {
							_, _ = fmt.Fprintf(out, "✗ Error: service '%s' depends on unknown service '%s'\n  Did you mean '%s'?\n", name, dep, bestMatch)
						} else {
							_, _ = fmt.Fprintf(out, "✗ Error: service '%s' depends on unknown service '%s'\n", name, dep)
						}
					}
				}
			}

			if !valid {
				return fmt.Errorf("validation failed due to missing references")
			}
			_, _ = fmt.Fprintf(out, "✓ All service references valid\n")

			eps := g.Entrypoints()
			if len(eps) == 1 {
				_, _ = fmt.Fprintf(out, "✓ 1 entrypoint found: %s\n", eps[0])
			} else {
				_, _ = fmt.Fprintf(out, "✓ %d entrypoints found\n", len(eps))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", ".diverge.yaml", "path to config file")

	return cmd
}
