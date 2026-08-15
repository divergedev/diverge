package testing

import (
	"net/http"
	"os"
	"time"

	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	Providers.Register("gitlab", registry.Provider[TestRunner]{
		Create: func(deps registry.Deps) (TestRunner, error) {
			token := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
			return &GitLabPipelineRunner{
				BaseURL:    "",
				Token:      token,
				HTTPClient: &http.Client{Timeout: 30 * time.Second},
			}, nil
		},
		Description: "GitLab CI pipeline runner",
	})
}
