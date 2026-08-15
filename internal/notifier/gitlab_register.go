package notifier

import (
	"flag"
	"fmt"
	"os"

	"github.com/divergedev/diverge/pkg/registry"
)

var (
	gitlabTokenFlag = flag.String("gitlab-token", "", "GitLab token for preview group notifier")
	gitlabURLFlag   = flag.String("gitlab-url", "", "GitLab URL for preview group notifier")
)

func init() {
	Providers.Register("gitlab", registry.Provider[Notifier]{
		Create: func(deps registry.Deps) (Notifier, error) {
			token := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
			if token == "" {
				return nil, fmt.Errorf("DIVERGE_NOTIFIER_TOKEN is required for --notifier-provider=gitlab")
			}
			return NewGitLabNotifier("", token), nil
		},
		Description: "GitLab MR notes and pipeline statuses",
	})

	StatusProviders.Register("gitlab", registry.Provider[StatusReporter]{
		Create: func(deps registry.Deps) (StatusReporter, error) {
			token := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
			if token == "" {
				return nil, fmt.Errorf("DIVERGE_NOTIFIER_TOKEN is required for --notifier-provider=gitlab")
			}
			return NewGitLabStatusReporter("", token), nil
		},
		Description: "GitLab status reporting",
	})

	GroupProviders.Register("gitlab", registry.Provider[PreviewGroupNotifier]{
		Create: func(deps registry.Deps) (PreviewGroupNotifier, error) {
			token := *gitlabTokenFlag
			if token == "" {
				token = os.Getenv("DIVERGE_GITLAB_TOKEN")
			}
			url := *gitlabURLFlag

			if (token != "" && url == "") || (token == "" && url != "") {
				return nil, fmt.Errorf("both --gitlab-token and --gitlab-url must be set together")
			}
			return NewGitLabPreviewGroupNotifier(url, token), nil
		},
		Description: "GitLab MR group comments",
	})
}
