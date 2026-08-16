package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func newLoginCmd(app *App) *cobra.Command {
	var token string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a Diverge server",
		Long:  "Authenticate with a Diverge server using a pre-provisioned token.",
		RunE: func(cmd *cobra.Command, args []string) error {
			serverURL := app.ServerURL
			if serverURL == "" {
				return fmt.Errorf("--server flag is required for login")
			}

			if token == "" {
				token = os.Getenv("DIVERGE_TOKEN")
			}

			if token != "" {
				// CI/CD mode: use pre-provisioned token
				fmt.Printf("Saving token for %s\n", serverURL)
				return saveCredentials(serverURL, token, "", time.Time{})
			}

			fmt.Println("Interactive OIDC login is not yet implemented.")
			fmt.Println("")
			fmt.Println("To authenticate, use one of:")
			fmt.Println("  diverge login --server <url> --token <token>")
			fmt.Println("  export DIVERGE_TOKEN=<token>")
			fmt.Println("")
			fmt.Println("OIDC browser login will be available in v0.8.0.")
			return fmt.Errorf("interactive login not yet implemented")
		},
	}

	cmd.Flags().StringVar(&token, "token", "", "Pre-provisioned bearer token (for CI/CD, prefer DIVERGE_TOKEN env var)")

	return cmd
}
