package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/divergedev/diverge/internal/config"
	"github.com/divergedev/diverge/pkg/topology"
)

type routeJSONOutput struct {
	Service string      `json:"service"`
	Paths   []routePath `json:"paths"`
	Header  string      `json:"header"`
}

type routePath struct {
	Hops      []string `json:"hops"`
	HopsCount int      `json:"hops_count"`
}

func newRouteCmd(app *App) *cobra.Command {
	var configPath string
	var gateway string
	var header string
	var output string
	var live bool

	cmd := &cobra.Command{
		Use:   "route <service>",
		Short: "Simulate request routing to a service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true

			if live {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "--live requires Prometheus configuration in .diverge.yaml")
				return nil
			}

			service := args[0]

			cfg, err := config.Load(configPath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("config file %s not found", configPath)
				}
				return fmt.Errorf("failed to load config: %w", err)
			}

			g := buildGraphFromConfig(cfg)
			out := cmd.OutOrStdout()

			// check if service exists
			found := false
			for _, n := range g.Services() {
				if n == service {
					found = true
					break
				}
			}

			var paths []topology.IngressPath
			if found {
				paths = g.AllIngressPaths(service)
			}

			if gateway == "" && term.IsTerminal(int(os.Stdout.Fd())) && output == "text" && len(paths) > 0 {
				gatewaySet := make(map[string]bool)
				for _, p := range paths {
					if len(p.Hops) > 0 {
						gatewaySet[p.Hops[0]] = true
					}
				}
				var gateways []string
				for gw := range gatewaySet {
					gateways = append(gateways, gw)
				}
				if len(gateways) > 1 {
					prompt := promptui.Select{
						Label: "Multiple gateways found. Select gateway to route through",
						Items: gateways,
					}
					_, result, err := prompt.Run()
					if err != nil {
						return fmt.Errorf("prompt failed: %w", err)
					}
					gateway = result
				}
			}

			if gateway != "" {
				var filtered []topology.IngressPath
				for _, p := range paths {
					if len(p.Hops) > 0 && p.Hops[0] == gateway {
						filtered = append(filtered, p)
					}
				}
				paths = filtered
			}

			if output == "json" {
				res := routeJSONOutput{
					Service: service,
					Paths:   []routePath{},
					Header:  header,
				}
				if res.Paths == nil {
					res.Paths = []routePath{}
				}
				for _, p := range paths {
					hops := p.Hops
					if hops == nil {
						hops = []string{}
					}
					res.Paths = append(res.Paths, routePath{
						Hops:      hops,
						HopsCount: len(p.Hops) - 1,
					})
				}
				b, err := json.MarshalIndent(res, "", "  ")
				if err != nil {
					return err
				}
				_, err = out.Write(append(b, '\n'))
				return err
			}

			// Text output
			_, _ = fmt.Fprintf(out, "Request routing for %q:\n\n", service)

			if !found || len(paths) == 0 {
				_, _ = fmt.Fprintf(out, "  Service is unreachable from any entrypoint.\n")
				return nil
			}

			for i, p := range paths {
				if i > 0 {
					_, _ = fmt.Fprintf(out, "\n---\n\n")
				}
				printPath(out, p.Hops, header)
			}

			_, _ = fmt.Fprintf(out, "\n  \u26A0 Ensure intermediate services propagate the routing header.\n")
			_, _ = fmt.Fprintf(out, "    See: https://docs.diverge.dev/guides/header-propagation\n")

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", ".diverge.yaml", "path to config file")
	cmd.Flags().StringVar(&gateway, "gateway", "", "select gateway")
	cmd.Flags().StringVar(&header, "header", "x-diverge-env", "routing header key")
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format (text, json)")
	cmd.Flags().BoolVar(&live, "live", false, "reserved flag for future Prometheus integration")

	return cmd
}

func printPath(out io.Writer, hops []string, header string) {
	_, _ = fmt.Fprintf(out, "  1. Client request with header: %s: <env-name>\n", header)
	step := 2
	for i, hop := range hops {
		if i == 0 {
			_, _ = fmt.Fprintf(out, "  %d. \u2192 %s (entrypoint)\n", step, hop)
			_, _ = fmt.Fprintf(out, "     Route: HTTPRoute matching header\n")
		} else if i == len(hops)-1 {
			_, _ = fmt.Fprintf(out, "  %d. \u2192 %s \u2713\n", step, hop)
		} else {
			_, _ = fmt.Fprintf(out, "  %d. \u2192 %s\n", step, hop)
			_, _ = fmt.Fprintf(out, "     Route: mesh sidecar forwards with header\n")
		}
		step++
	}

	_, _ = fmt.Fprintf(out, "\n  Path: %s (%d hops)\n", strings.Join(hops, " \u2192 "), len(hops)-1)
	_, _ = fmt.Fprintf(out, "  Header: %s propagated at each hop \u2713\n", header)
}
