package notifier

import (
	"fmt"
	"os"

	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	Providers.Register("github", registry.Provider[Notifier]{
		Create: func(deps registry.Deps) (Notifier, error) {
			token := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
			if token == "" {
				return nil, fmt.Errorf("DIVERGE_NOTIFIER_TOKEN is required for --notifier-provider=github")
			}
			return NewGitHubNotifier("", token), nil
		},
		Description: "GitHub commit statuses and PR comments",
	})

	StatusProviders.Register("github", registry.Provider[StatusReporter]{
		Create: func(deps registry.Deps) (StatusReporter, error) {
			token := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
			if token == "" {
				return nil, fmt.Errorf("DIVERGE_NOTIFIER_TOKEN is required for --notifier-provider=github")
			}
			return NewGitHubStatusReporter("", token), nil
		},
		Description: "GitHub status reporting",
	})

	GroupProviders.Register("github", registry.Provider[PreviewGroupNotifier]{
		Create: func(deps registry.Deps) (PreviewGroupNotifier, error) {
			token := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
			return NewGitHubPreviewGroupNotifier("", token), nil
		},
		Description: "GitHub PR group comments",
	})
}
