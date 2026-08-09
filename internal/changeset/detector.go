package changeset

import "context"

// ChangeDetector is responsible for determining which services have changed
type ChangeDetector interface {
	DetectChanges(ctx context.Context, repo, branch, baseRef string, servicePaths map[string][]string) ([]string, error)
}

type GitChangeDetector struct {
	// Add config for mapping paths to services
}

func (g *GitChangeDetector) DetectChanges(ctx context.Context, repo, branch, baseRef string, servicePaths map[string][]string) ([]string, error) {
	// Mock implementation
	// In reality, this would run git diff and match file paths against a configured service map
	return []string{}, nil
}
