package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func newEnvCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Manage environment variables",
		Long:  `View and export environment variables for services.`,
	}
	cmd.AddCommand(newEnvExportCmd(app))
	return cmd
}

func newEnvExportCmd(app *App) *cobra.Command {
	var service string
	var format string
	var output string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export environment variables for a service",
		Long: `Export environment variables from a baseline pod.
Supports multiple formats: dotenv, json, shell.`,
		Example: `  diverge env export --service payments --format dotenv > .env.preview
  diverge env export --service payments --format json
  diverge env export --service payments --format shell`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if service == "" {
				return fmt.Errorf("service flag is required")
			}

			_, clientset, err := app.KubeClient()
			if err != nil {
				return fmt.Errorf("failed to create Kubernetes client: %w", err)
			}

			ns := app.Namespace
			if ns == "" {
				ns = "default"
			}

			pod, err := findBaselinePod(cmd.Context(), clientset, ns, service)
			if err != nil {
				return fmt.Errorf("failed to find baseline pod: %w", err)
			}

			resolvedEnv, err := resolveBaselineEnv(cmd.Context(), clientset, pod)
			if err != nil {
				return fmt.Errorf("failed to resolve baseline env: %w", err)
			}

			var outStr string
			switch format {
			case "dotenv":
				outStr, err = formatDotenv(resolvedEnv)
			case "json":
				outStr, err = formatJSON(resolvedEnv)
			case "shell":
				outStr, err = formatShell(resolvedEnv)
			default:
				return fmt.Errorf("unsupported format: %q (must be dotenv, json, or shell)", format)
			}
			if err != nil {
				return fmt.Errorf("failed to format output: %w", err)
			}

			if output != "" {
				if err := os.WriteFile(output, []byte(outStr), 0600); err != nil {
					return fmt.Errorf("failed to write output file: %w", err)
				}
			} else {
				fmt.Print(outStr)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&service, "service", "", "Service name")
	cmd.Flags().StringVar(&format, "format", "dotenv", "Output format: dotenv, json, shell")
	cmd.Flags().StringVar(&output, "output", "", "Output file (default: stdout)")
	_ = cmd.MarkFlagRequired("service")

	return cmd
}

func formatDotenv(env map[string]string) (string, error) {
	if len(env) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for _, k := range keys {
		v := env[k]
		if strings.Contains(v, " ") || strings.Contains(v, "\n") || strings.Contains(v, "\"") {
			fmt.Fprintf(&sb, "%s=%q\n", k, v)
		} else {
			fmt.Fprintf(&sb, "%s=%s\n", k, v)
		}
	}
	return sb.String(), nil
}

func formatJSON(env map[string]string) (string, error) {
	if len(env) == 0 {
		return "{}\n", nil
	}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

func formatShell(env map[string]string) (string, error) {
	if len(env) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for _, k := range keys {
		v := env[k]
		fmt.Fprintf(&sb, "export %s=%q\n", k, v)
	}
	return sb.String(), nil
}
