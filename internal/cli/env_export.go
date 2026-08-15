package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	var groupName string
	var format string
	var output string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export environment variables for a service",
		Long: `Export environment variables from a baseline pod.
Supports multiple formats: dotenv, json, shell.`,
		Example: `  diverge env export --service payments --format dotenv > .env.preview
  diverge env export --group mr-42 --format json
  diverge env export --group mr-42 --service payments --format shell`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if service == "" && groupName == "" {
				return fmt.Errorf("either --service or --group flag is required")
			}

			c, clientset, err := app.KubeClient()
			if err != nil {
				return fmt.Errorf("failed to create Kubernetes client: %w", err)
			}

			ns := app.Namespace
			if ns == "" {
				ns = "default"
			}

			var childEnv *divergeiov1alpha1.Environment

			if groupName != "" {
				var pg divergeiov1alpha1.PreviewGroup
				if err := c.Get(cmd.Context(), client.ObjectKey{Name: groupName}, &pg); err != nil {
					return fmt.Errorf("failed to get PreviewGroup %q: %w", groupName, err)
				}

				var targetSvc *divergeiov1alpha1.PreviewGroupServiceStatus
				if service != "" {
					for i, s := range pg.Status.Services {
						if s.Name == service {
							targetSvc = &pg.Status.Services[i]
							break
						}
					}
					if targetSvc == nil {
						return fmt.Errorf("service %q not found in PreviewGroup %q", service, groupName)
					}
				} else {
					if len(pg.Status.Services) == 1 {
						targetSvc = &pg.Status.Services[0]
						service = targetSvc.Name
					} else if len(pg.Status.Services) > 1 {
						var svcNames []string
						for _, s := range pg.Status.Services {
							svcNames = append(svcNames, s.Name)
						}
						return fmt.Errorf("PreviewGroup %q has multiple services (%s) please specify one using --service", groupName, strings.Join(svcNames, ", "))
					} else {
						return fmt.Errorf("PreviewGroup %q has no services", groupName)
					}
				}

				if targetSvc.EnvironmentName == "" {
					return fmt.Errorf("service %q has no Environment created yet", service)
				}

				envNS := targetSvc.Namespace
				if envNS == "" {
					envNS = ns
				}

				var env divergeiov1alpha1.Environment
				if err := c.Get(cmd.Context(), client.ObjectKey{Name: targetSvc.EnvironmentName, Namespace: envNS}, &env); err != nil {
					return fmt.Errorf("failed to get Environment %q: %w", targetSvc.EnvironmentName, err)
				}
				childEnv = &env
			}

			pod, err := findBaselinePod(cmd.Context(), clientset, ns, service)
			if err != nil {
				return fmt.Errorf("failed to find baseline pod: %w", err)
			}

			resolvedEnv, err := resolveBaselineEnv(cmd.Context(), clientset, pod)
			if err != nil {
				return fmt.Errorf("failed to resolve baseline env: %w", err)
			}

			if childEnv != nil && childEnv.Spec.ServiceConfig != nil {
				for _, envVar := range childEnv.Spec.ServiceConfig.Env {
					resolvedEnv[envVar.Name] = envVar.Value
				}
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
				_, _ = fmt.Fprint(cmd.OutOrStdout(), outStr)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&service, "service", "", "Service name")
	cmd.Flags().StringVarP(&groupName, "group", "g", "", "PreviewGroup name to export env vars from")
	cmd.Flags().StringVar(&groupName, "preview-group", "", "PreviewGroup name to export env vars from (alias for --group)")
	_ = cmd.Flags().MarkHidden("preview-group")
	cmd.Flags().StringVar(&format, "format", "dotenv", "Output format: dotenv, json, shell")
	cmd.Flags().StringVar(&output, "output", "", "Output file (default: stdout)")

	_ = cmd.RegisterFlagCompletionFunc("group", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		c, _, err := app.KubeClient()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var pgList divergeiov1alpha1.PreviewGroupList
		if err := c.List(cmd.Context(), &pgList); err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var names []string
		for _, pg := range pgList.Items {
			if strings.HasPrefix(pg.Name, toComplete) {
				names = append(names, pg.Name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	})

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
