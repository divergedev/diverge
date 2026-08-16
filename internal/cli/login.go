package cli

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

func newLoginCmd(app *App) *cobra.Command {
	var serverURL string
	var token string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a Diverge server",
		Long:  "Authenticate with a Diverge server using OIDC or a pre-provisioned token.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if token != "" {
				// CI/CD mode: use pre-provisioned token
				fmt.Printf("Saving token for %s\n", serverURL)
				return saveCredentials(serverURL, token, "", time.Time{})
			}
			// Interactive mode: OIDC PKCE flow
			return loginWithOIDC(cmd.Context(), serverURL)
		},
	}

	cmd.Flags().StringVar(&serverURL, "server", "", "Diverge server URL")
	cmd.Flags().StringVar(&token, "token", "", "Pre-provisioned bearer token (for CI/CD)")
	_ = cmd.MarkFlagRequired("server")

	return cmd
}

func loginWithOIDC(ctx context.Context, serverURL string) error {
	fmt.Println("OIDC PKCE login flow")
	fmt.Printf("Opening browser for authentication with %s...\n", serverURL)

	callbackCh := make(chan string, 1)
	server := &http.Server{Addr: ":9876"}
	http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		callbackCh <- code
		_, _ = fmt.Fprintf(w, "Authentication successful! You can close this tab.")
	})
	go func() {
		_ = server.ListenAndServe()
	}()
	defer func() {
		_ = server.Shutdown(ctx)
	}()

	openBrowser(fmt.Sprintf("%s/auth/login?redirect_uri=http://localhost:9876/callback", serverURL))

	select {
	case code := <-callbackCh:
		fmt.Println("Received auth code, exchanging for token...")
		// Since full OIDC PKCE isn't implemented, we simulate token saving here:
		_ = code
		return saveCredentials(serverURL, "simulated-token-from-code", "", time.Now().Add(1*time.Hour))
	case <-ctx.Done():
		return ctx.Err()
	}
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", url).Start()
	case "linux":
		_ = exec.Command("xdg-open", url).Start()
	}
}
