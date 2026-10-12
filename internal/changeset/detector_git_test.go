package changeset

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cleanGitEnv(os.Environ()),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(out))
}

func TestGitChangeDetector_DetectChanges_NormalDiffAndFallback(t *testing.T) {
	// Simulate git hook environment variables to ensure subprocess git commands ignore them
	t.Setenv("GIT_DIR", "/nonexistent/.git")
	t.Setenv("GIT_WORK_TREE", "/nonexistent")
	t.Setenv("GIT_INDEX_FILE", "/nonexistent/index")

	ctx := context.Background()
	repoDir := t.TempDir()

	// Initialize temporary git repository with main branch
	runGitCmd(t, repoDir, "init", "-b", "main")

	// Commit baseline files on main
	apiDir := filepath.Join(repoDir, "services", "api")
	paymentsDir := filepath.Join(repoDir, "services", "payments")
	workerDir := filepath.Join(repoDir, "services", "worker")
	require.NoError(t, os.MkdirAll(apiDir, 0o755))
	require.NoError(t, os.MkdirAll(paymentsDir, 0o755))
	require.NoError(t, os.MkdirAll(workerDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(apiDir, "main.go"), []byte("package main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(paymentsDir, "charge.go"), []byte("package payments\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workerDir, "job.go"), []byte("package worker\n"), 0o644))
	runGitCmd(t, repoDir, "add", ".")
	runGitCmd(t, repoDir, "commit", "-m", "initial commit on main")

	// Create feature branch and modify api + payments
	runGitCmd(t, repoDir, "checkout", "-b", "feat/checkout-flow")
	require.NoError(t, os.WriteFile(filepath.Join(apiDir, "main.go"), []byte("package main\n// updated\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(paymentsDir, "charge.go"), []byte("package payments\n// updated\n"), 0o644))
	runGitCmd(t, repoDir, "add", ".")
	runGitCmd(t, repoDir, "commit", "-m", "update api and payments")

	servicePaths := map[string][]string{
		"api":      {"services/api"},
		"payments": {"services/payments"},
		"worker":   {"services/worker"},
	}

	detector := &GitChangeDetector{WorkDir: repoDir}

	// 1. Normal three-dot diff (main...HEAD)
	changed, err := detector.DetectChanges(ctx, "org/repo", "feat/checkout-flow", "main", servicePaths)
	require.NoError(t, err)
	assert.Equal(t, []string{"api", "payments"}, changed)

	// 2. Fallback two-dot diff when three-dot diff fails (orphan branch has no merge-base with HEAD)
	runGitCmd(t, repoDir, "checkout", "--orphan", "orphan-base")
	runGitCmd(t, repoDir, "rm", "-rf", ".")
	require.NoError(t, os.MkdirAll(workerDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workerDir, "job.go"), []byte("package worker\n// orphan version\n"), 0o644))
	runGitCmd(t, repoDir, "add", ".")
	runGitCmd(t, repoDir, "commit", "-m", "orphan base commit")

	// Switch back to feature branch and diff against orphan-base (no merge base -> triggers fallback)
	runGitCmd(t, repoDir, "checkout", "feat/checkout-flow")
	changedFallback, err := detector.DetectChanges(ctx, "org/repo", "feat/checkout-flow", "orphan-base", servicePaths)
	require.NoError(t, err)
	assert.Equal(t, []string{"api", "payments", "worker"}, changedFallback)

	// 3. Invalid baseRef returns error
	_, err = detector.DetectChanges(ctx, "org/repo", "feat/checkout-flow", "nonexistent-ref", servicePaths)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "git diff failed")
}

func TestCleanGitEnv(t *testing.T) {
	input := []string{
		"PATH=/usr/bin",
		"GIT_DIR=/outer/.git",
		"GIT_WORK_TREE=/outer",
		"GIT_INDEX_FILE=/outer/.git/index",
		"GIT_OBJECT_DIRECTORY=/outer/.git/objects",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=/alt",
		"GIT_COMMON_DIR=/common",
		"GIT_PREFIX=sub/",
		"HOME=/tmp",
	}
	got := cleanGitEnv(input)
	assert.Equal(t, []string{"PATH=/usr/bin", "HOME=/tmp"}, got)
}
