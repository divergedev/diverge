package changeset

// Detector is responsible for determining which services have changed
type Detector interface {
	Detect(repoURL, branch string) ([]string, error)
}

type GitDetector struct {
	// Add config for mapping paths to services
}

func (g *GitDetector) Detect(repoURL, branch string) ([]string, error) {
	// Mock implementation
	// In reality, this would run git diff and match file paths against a configured service map
	return []string{}, nil
}
