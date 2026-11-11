package testing

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	Providers.Register("github", registry.Provider[TestRunner]{
		Create: func(deps registry.Deps) (TestRunner, error) {
			token := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
			baseURL := strings.TrimSpace(os.Getenv("DIVERGE_NOTIFIER_BASE_URL"))
			if baseURL == "" {
				baseURL = strings.TrimSpace(os.Getenv("GITHUB_API_URL"))
			}
			return &GitHubActionsRunner{
				BaseURL:    baseURL,
				Token:      token,
				HTTPClient: &http.Client{Timeout: 30 * time.Second},
			}, nil
		},
		Description: "GitHub Actions test runner",
	})
}
