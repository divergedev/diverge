package notifier

import (
	"fmt"
	"os"
	"strings"

	"github.com/divergedev/diverge/pkg/registry"
)

func githubBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("DIVERGE_BASE_URL")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("GITHUB_API_URL"))
}

func init() {
	Providers.Register("github", registry.Provider[Notifier]{
		Create: func(deps registry.Deps) (Notifier, error) {
			token := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
			if token == "" {
				return nil, fmt.Errorf("DIVERGE_NOTIFIER_TOKEN is required for --notifier-provider=github")
			}
			return NewGitHubNotifier(githubBaseURL(), token), nil
		},
		Description: "GitHub commit statuses and PR comments",
	})

	StatusProviders.Register("github", registry.Provider[StatusReporter]{
		Create: func(deps registry.Deps) (StatusReporter, error) {
			token := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
			if token == "" {
				return nil, fmt.Errorf("DIVERGE_NOTIFIER_TOKEN is required for --notifier-provider=github")
			}
			return NewGitHubStatusReporter(githubBaseURL(), token), nil
		},
		Description: "GitHub status reporting",
	})

	GroupProviders.Register("github", registry.Provider[PreviewGroupNotifier]{
		Create: func(deps registry.Deps) (PreviewGroupNotifier, error) {
			token := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
			return NewGitHubPreviewGroupNotifier(githubBaseURL(), token), nil
		},
		Description: "GitHub PR group comments",
	})
}
