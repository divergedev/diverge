package git

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// GitContext holds the detected git repository context.
type GitContext struct {
	Provider  string // "gitlab" or "github"
	Project   string // "org/repo" or "group/subgroup/repo"
	Branch    string // current branch name
	RemoteURL string // raw remote URL
}

// Detect auto-detects git context from the current working directory.
// It runs git commands to determine the current branch and remote URL.
func Detect() (*GitContext, error) {
	branch, err := runGit("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to detect branch: %w", err)
	}

	remoteURL, err := runGit("remote", "get-url", "origin")
	if err != nil {
		return nil, fmt.Errorf("failed to detect remote: %w", err)
	}

	provider, project, err := ParseRemoteURL(remoteURL)
	if err != nil {
		return nil, err
	}

	return &GitContext{
		Provider:  provider,
		Project:   project,
		Branch:    branch,
		RemoteURL: remoteURL,
	}, nil
}

// ParseRemoteURL extracts the provider and project from a git remote URL.
// Supports SSH and HTTPS formats for GitHub and GitLab (including subgroups).
func ParseRemoteURL(rawURL string) (provider, project string, err error) {
	if rawURL == "" {
		return "", "", errors.New("invalid URL: empty")
	}

	var host, path string

	// SSH format: git@host:path
	sshRe := regexp.MustCompile(`^git@[^:]+:(.+)$`)
	if match := sshRe.FindStringSubmatch(rawURL); match != nil {
		hostStr := strings.Split(strings.TrimPrefix(rawURL, "git@"), ":")[0]
		host = hostStr
		path = match[1]
	} else if strings.HasPrefix(rawURL, "https://") || strings.HasPrefix(rawURL, "http://") {
		// HTTPS format
		urlStr := strings.TrimPrefix(rawURL, "https://")
		urlStr = strings.TrimPrefix(urlStr, "http://")
		parts := strings.SplitN(urlStr, "/", 2)
		if len(parts) == 2 {
			host = parts[0]
			path = parts[1]
		}
	}

	if host == "" || path == "" {
		return "", "", fmt.Errorf("could not parse remote URL: %s", rawURL)
	}

	path = strings.TrimSuffix(path, ".git")

	switch host {
	case "github.com":
		provider = "github"
	case "gitlab.com":
		provider = "gitlab"
	case "bitbucket.org":
		provider = "bitbucket"
	default:
		// Self-hosted instances: default to gitlab since it's the most
		// common self-hosted Git platform. Users can override via config.
		provider = "gitlab"
	}

	return provider, path, nil
}

// SlugifyBranch converts a branch name to a URL-safe slug.
// e.g., "feat/my-feature" → "feat-my-feature"
func SlugifyBranch(branch string) string {
	// Replace non-alphanumeric chars with hyphens
	re := regexp.MustCompile(`[^a-zA-Z0-9]+`)
	slug := re.ReplaceAllString(branch, "-")

	// Trim leading/trailing hyphens
	slug = strings.Trim(slug, "-")

	// Lowercase
	slug = strings.ToLower(slug)

	// Truncate to 63 chars (K8s label limit)
	if len(slug) > 63 {
		slug = slug[:63]
		// Trim again in case we cut at a hyphen
		slug = strings.TrimRight(slug, "-")
	}

	return slug
}

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
