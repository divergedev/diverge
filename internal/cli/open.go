package cli

import (
	"fmt"
	urlPkg "net/url"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

var openBrowser = defaultOpenBrowser

func defaultOpenBrowser(targetURL string) error {
	var execCmd string
	var execArgs []string

	switch runtime.GOOS {
	case "linux":
		execCmd = "xdg-open"
		execArgs = []string{targetURL}
	case "windows":
		execCmd = "rundll32"
		execArgs = []string{"url.dll,FileProtocolHandler", targetURL}
	case "darwin":
		execCmd = "open"
		execArgs = []string{targetURL}
	default:
		return fmt.Errorf("unsupported platform")
	}

	return exec.Command(execCmd, execArgs...).Start()
}

func buildPreviewURL(env *divergeiov1alpha1.Environment, magic ...bool) (string, error) {
	if env == nil || env.Status.URL == "" {
		return "", fmt.Errorf("environment URL is not set")
	}

	parsed, err := urlPkg.Parse(env.Status.URL)
	if err != nil {
		return "", fmt.Errorf("invalid environment URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("refusing to open URL with scheme %q (only http/https allowed)", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("environment URL %q has no hostname", env.Status.URL)
	}

	useMagic := len(magic) > 0 && magic[0]
	if useMagic {
		headerVal := env.Spec.Routing.HeaderValue
		if headerVal == "" {
			headerVal = env.Name
		}
		q := parsed.Query()
		q.Set("_diverge_env", headerVal)
		parsed.RawQuery = q.Encode()
		return parsed.String(), nil
	}

	if env.Spec.Routing.Mode == "header" {
		parsed.Host = fmt.Sprintf("%s.preview.%s", env.Name, parsed.Host)
		return parsed.String(), nil
	}

	return env.Status.URL, nil
}

func buildPreviewGroupURL(pg *divergeiov1alpha1.PreviewGroup, magic ...bool) (string, error) {
	if pg == nil {
		return "", fmt.Errorf("preview group URL is not set")
	}

	rawURL := strings.TrimSpace(pg.Spec.Routing.ExternalURL)
	if rawURL == "" {
		for _, svc := range pg.Status.Services {
			if strings.TrimSpace(svc.URL) != "" {
				rawURL = strings.TrimSpace(svc.URL)
				break
			}
		}
	}
	if rawURL == "" {
		return "", fmt.Errorf("preview group URL is not set")
	}

	parsed, err := urlPkg.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid preview group URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("refusing to open URL with scheme %q (only http/https allowed)", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("preview group URL %q has no hostname", rawURL)
	}

	useMagic := len(magic) > 0 && magic[0]
	if useMagic {
		headerVal := pg.Spec.Routing.HeaderValue
		if headerVal == "" {
			headerVal = pg.Name
		}
		q := parsed.Query()
		q.Set("_diverge_env", headerVal)
		parsed.RawQuery = q.Encode()
		return parsed.String(), nil
	}

	if pg.Spec.Routing.Mode == "header" || pg.Spec.Routing.Mode == "" {
		prefix := pg.Name + ".preview."
		if !strings.HasPrefix(parsed.Host, prefix) {
			parsed.Host = fmt.Sprintf("%s.preview.%s", pg.Name, parsed.Host)
		}
		return parsed.String(), nil
	}

	return rawURL, nil
}

func newOpenCmd(app *App) *cobra.Command {
	var magic bool
	var noBrowser bool

	cmd := &cobra.Command{
		Use:               "open <name>",
		Short:             "Open a preview Environment or PreviewGroup URL in the browser",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeEnvironmentNames(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.KubeClient()
			if err != nil {
				return err
			}

			name := args[0]
			var targetURL string
			var env divergeiov1alpha1.Environment
			if err := c.Get(cmd.Context(), client.ObjectKey{Name: name, Namespace: app.Namespace}, &env); err == nil {
				targetURL, err = buildPreviewURL(&env, magic)
				if err != nil {
					return err
				}
				if noBrowser {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), targetURL)
					return nil
				}
				if env.Spec.Routing.Mode == "header" {
					headerKey := env.Spec.Routing.HeaderKey
					if headerKey == "" {
						headerKey = "x-diverge-env"
					}
					headerVal := env.Spec.Routing.HeaderValue
					if headerVal == "" {
						headerVal = env.Name
					}
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Note: Environment uses header routing. You can also curl it like this:\n")
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "diverge curl %s\n", env.Name)
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "curl -H \"%s: %s\" %s\n\n", headerKey, headerVal, env.Status.URL)
				}
			} else {
				var pg divergeiov1alpha1.PreviewGroup
				pgErr := c.Get(cmd.Context(), client.ObjectKey{Name: name, Namespace: app.Namespace}, &pg)
				if pgErr != nil {
					pgErr = c.Get(cmd.Context(), client.ObjectKey{Name: name}, &pg)
				}
				if pgErr != nil {
					return fmt.Errorf("failed to get environment %s: %w", name, err)
				}

				targetURL, err = buildPreviewGroupURL(&pg, magic)
				if err != nil {
					return err
				}
				if noBrowser {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), targetURL)
					return nil
				}
				if pg.Spec.Routing.Mode == "header" || pg.Spec.Routing.Mode == "" {
					headerKey := pg.Spec.Routing.HeaderKey
					if headerKey == "" {
						headerKey = "x-diverge-env"
					}
					headerVal := pg.Spec.Routing.HeaderValue
					if headerVal == "" {
						headerVal = pg.Name
					}
					rawURL := pg.Spec.Routing.ExternalURL
					if rawURL == "" {
						for _, svc := range pg.Status.Services {
							if svc.URL != "" {
								rawURL = svc.URL
								break
							}
						}
					}
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Note: PreviewGroup uses header routing. You can also curl it like this:\n")
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "diverge curl %s\n", pg.Name)
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "curl -H \"%s: %s\" %s\n\n", headerKey, headerVal, rawURL)
				}
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Opening %s\n", targetURL)
			return openBrowser(targetURL)
		},
	}

	cmd.Flags().BoolVarP(&magic, "magic", "m", false, "Open the cookie-setting magic URL (?_diverge_env=<env>)")
	cmd.Flags().BoolVarP(&noBrowser, "no-browser", "p", false, "Print the resolved preview URL without launching a browser")

	return cmd
}
