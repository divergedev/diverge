package testing

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	Providers.Register("gitlab", registry.Provider[TestRunner]{
		Create: func(deps registry.Deps) (TestRunner, error) {
			token := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
			baseURL := strings.TrimSpace(os.Getenv("DIVERGE_BASE_URL"))
			if baseURL == "" {
				baseURL = strings.TrimSpace(os.Getenv("GITLAB_API_URL"))
			}
			return &GitLabPipelineRunner{
				BaseURL:    baseURL,
				Token:      token,
				HTTPClient: &http.Client{Timeout: 30 * time.Second},
			}, nil
		},
		Description: "GitLab CI pipeline runner",
	})
}
