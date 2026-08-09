package changeset

import (
	"context"
	"testing"
)

func TestGitChangeDetector(t *testing.T) {
	detector := &GitChangeDetector{}

	servicePaths := map[string][]string{
		"service-a": {"src/service-a"},
		"service-b": {"src/service-b"},
	}

	changes, err := detector.DetectChanges(context.Background(), "repo", "branch", "base", servicePaths)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if len(changes) != 0 {
		t.Errorf("Expected 0 changes initially, got %d", len(changes))
	}
}
