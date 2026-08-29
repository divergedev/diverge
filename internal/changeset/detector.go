package changeset

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ChangeDetector is responsible for determining which services have changed
type ChangeDetector interface {
	DetectChanges(ctx context.Context, repo, branch, baseRef string, servicePaths map[string][]string) ([]string, error)
}

// GitChangeDetector detects changed services by running git diff and matching
// changed file paths against the configured service path prefixes.
type GitChangeDetector struct {
	// WorkDir is the git working directory. If empty, uses current directory.
	WorkDir string
}

// DetectChanges runs git diff between baseRef and HEAD (or branch), then matches
// changed files against servicePaths to determine which services are affected.
func (g *GitChangeDetector) DetectChanges(ctx context.Context, repo, branch, baseRef string, servicePaths map[string][]string) ([]string, error) {
	changedFiles, err := g.gitDiffFiles(ctx, baseRef)
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	return matchServicesToFiles(changedFiles, servicePaths), nil
}

// DetectChangesFromFiles matches pre-computed changed files against service paths.
// Useful when the caller already has the file list (e.g., from GitHub API).
func DetectChangesFromFiles(changedFiles []string, servicePaths map[string][]string) []string {
	return matchServicesToFiles(changedFiles, servicePaths)
}

func (g *GitChangeDetector) gitDiffFiles(ctx context.Context, baseRef string) ([]string, error) {
	// Bound git operations to prevent hanging on stalled processes
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	args := []string{"diff", "--name-only", baseRef + "...HEAD"}
	cmd := exec.CommandContext(ctx, "git", args...)
	if g.WorkDir != "" {
		cmd.Dir = g.WorkDir
	}

	out, err := cmd.Output()
	if err != nil {
		// Fallback: try two-dot diff if three-dot fails (no merge base)
		args = []string{"diff", "--name-only", baseRef}
		cmd = exec.CommandContext(ctx, "git", args...)
		if g.WorkDir != "" {
			cmd.Dir = g.WorkDir
		}
		out, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git diff %s: %w", baseRef, err)
		}
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

func matchServicesToFiles(changedFiles []string, servicePaths map[string][]string) []string {
	seen := make(map[string]bool)
	var changed []string

	for _, file := range changedFiles {
		for svcName, paths := range servicePaths {
			if seen[svcName] {
				continue
			}
			if matchesServicePaths(file, paths) {
				seen[svcName] = true
				changed = append(changed, svcName)
			}
		}
	}

	// Sort for deterministic output
	sortStrings(changed)
	return changed
}

func matchesServicePaths(file string, paths []string) bool {
	if len(paths) == 0 {
		// No paths configured — any change matches (implicit root)
		return true
	}
	for _, prefix := range paths {
		prefix = filepath.Clean(prefix)
		if prefix == "." || prefix == "" {
			return true
		}
		// Match if file is under the path prefix
		if strings.HasPrefix(file, prefix+"/") || file == prefix {
			return true
		}
		// Glob match
		if matched, _ := filepath.Match(prefix, file); matched {
			return true
		}
	}
	return false
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
