package notifier

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/divergedev/diverge/pkg/registry"
)

var (
	gitlabTokenFlag = flag.String("gitlab-token", "", "GitLab token for preview group notifier")
	gitlabURLFlag   = flag.String("gitlab-url", "", "GitLab URL for preview group notifier")
)

func gitlabBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("DIVERGE_BASE_URL")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("GITLAB_API_URL"))
}

func init() {
	Providers.Register("gitlab", registry.Provider[Notifier]{
		Create: func(deps registry.Deps) (Notifier, error) {
			token := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
			if token == "" {
				return nil, fmt.Errorf("DIVERGE_NOTIFIER_TOKEN is required for --notifier-provider=gitlab")
			}
			return NewGitLabNotifier(gitlabBaseURL(), token), nil
		},
		Description: "GitLab MR notes and pipeline statuses",
	})

	StatusProviders.Register("gitlab", registry.Provider[StatusReporter]{
		Create: func(deps registry.Deps) (StatusReporter, error) {
			token := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
			if token == "" {
				return nil, fmt.Errorf("DIVERGE_NOTIFIER_TOKEN is required for --notifier-provider=gitlab")
			}
			return NewGitLabStatusReporter(gitlabBaseURL(), token), nil
		},
		Description: "GitLab status reporting",
	})

	GroupProviders.Register("gitlab", registry.Provider[PreviewGroupNotifier]{
		Create: func(deps registry.Deps) (PreviewGroupNotifier, error) {
			token := strings.TrimSpace(*gitlabTokenFlag)
			if token == "" {
				token = strings.TrimSpace(os.Getenv("DIVERGE_GITLAB_TOKEN"))
			}
			if token == "" {
				token = strings.TrimSpace(os.Getenv("DIVERGE_NOTIFIER_TOKEN"))
			}
			url := strings.TrimSpace(*gitlabURLFlag)
			if url == "" {
				url = gitlabBaseURL()
			}

			if *gitlabURLFlag != "" && token == "" {
				return nil, fmt.Errorf("gitlab token is required when --gitlab-url is set")
			}
			if token == "" {
				return &NoopPreviewGroupNotifier{}, nil
			}
			return NewGitLabPreviewGroupNotifier(url, token), nil
		},
		Description: "GitLab MR group comments",
	})
}
