package cli

import (
	"fmt"
	urlPkg "net/url"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func newOpenCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{

		Use:   "open <name>",
		Short: "Open the environment URL in the browser",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.KubeClient()
			if err != nil {
				return err
			}

			name := args[0]
			var env divergeiov1alpha1.Environment
			if err := c.Get(cmd.Context(), client.ObjectKey{Name: name, Namespace: app.Namespace}, &env); err != nil {
				return fmt.Errorf("failed to get environment %s: %w", name, err)
			}

			url := env.Status.URL
			if url == "" {
				return fmt.Errorf("environment URL is not set")
			}

			// Validate URL scheme to prevent opening dangerous URIs
			parsed, err := urlPkg.Parse(url)
			if err != nil {
				return fmt.Errorf("invalid environment URL: %w", err)
			}
			if parsed.Scheme != "http" && parsed.Scheme != "https" {
				return fmt.Errorf("refusing to open URL with scheme %q (only http/https allowed)", parsed.Scheme)
			}
			if parsed.Hostname() == "" {
				return fmt.Errorf("environment URL %q has no hostname", url)
			}

			if env.Spec.Routing.Mode == "header" {
				// Construct magic URL for opening
				u, err := urlPkg.Parse(env.Status.URL)
				if err != nil || u.Host == "" {
					fmt.Printf("Note: Environment uses header routing. You can curl it like this:\n")
					fmt.Printf("curl -H \"%s: %s\" %s\n", env.Spec.Routing.HeaderKey, env.Spec.Routing.HeaderValue, env.Status.URL)
					return nil
				}
				u.Host = fmt.Sprintf("%s.preview.%s", env.Name, u.Host)
				url = u.String()
				fmt.Printf("Note: Environment uses header routing. You can also curl it like this:\n")
				fmt.Printf("curl -H \"%s: %s\" %s\n\n", env.Spec.Routing.HeaderKey, env.Spec.Routing.HeaderValue, env.Status.URL)
			}

			fmt.Printf("Opening %s\n", url)

			var execCmd string
			var execArgs []string

			switch runtime.GOOS {
			case "linux":
				execCmd = "xdg-open"
				execArgs = []string{url}
			case "windows":
				execCmd = "rundll32"
				execArgs = []string{"url.dll,FileProtocolHandler", url}
			case "darwin":
				execCmd = "open"
				execArgs = []string{url}
			default:
				return fmt.Errorf("unsupported platform")
			}

			return exec.Command(execCmd, execArgs...).Start()
		},
	}

	return cmd
}
