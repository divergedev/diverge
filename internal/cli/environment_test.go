package cli

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultEnvironmentDetector_DetectUsername(t *testing.T) {
	d := &DefaultEnvironmentDetector{}
	username, err := d.DetectUsername(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, username)
}

func TestDefaultEnvironmentDetector_DetectServiceName(t *testing.T) {
	d := &DefaultEnvironmentDetector{}
	ctx := context.Background()

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.Chdir(origDir)
	})

	t.Run("from .diverge.yaml", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgContent := "version: \"1\"\nservices:\n  billing-service:\n    image:\n      repository: ghcr.io/org/billing\n      tag_template: \"{{.Branch}}\"\n"
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".diverge.yaml"), []byte(cfgContent), 0o600))
		require.NoError(t, os.Chdir(tmpDir))

		svc, err := d.DetectServiceName(ctx)
		require.NoError(t, err)
		assert.Equal(t, "billing-service", svc)
	})

	t.Run("fallback to directory basename", func(t *testing.T) {
		tmpDir := t.TempDir()
		require.NoError(t, os.Chdir(tmpDir))

		svc, err := d.DetectServiceName(ctx)
		require.NoError(t, err)
		assert.Equal(t, filepath.Base(tmpDir), svc)
	})
}

func TestDefaultEnvironmentDetector_DetectGitBranch(t *testing.T) {
	d := &DefaultEnvironmentDetector{}
	ctx := context.Background()

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.Chdir(origDir)
	})

	// Ensure GIT_DIR and GIT_WORK_TREE from outer git hooks don't interfere
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		if val, ok := os.LookupEnv(k); ok {
			origVal := val
			key := k
			require.NoError(t, os.Unsetenv(key))
			t.Cleanup(func() {
				_ = os.Setenv(key, origVal)
			})
		}
	}

	t.Run("outside git repository returns ErrNoGitRepo", func(t *testing.T) {
		tmpDir := t.TempDir()
		require.NoError(t, os.Chdir(tmpDir))

		branch, err := d.DetectGitBranch(ctx)
		assert.Empty(t, branch)
		assert.ErrorIs(t, err, ErrNoGitRepo)
	})

	t.Run("inside initialized git repository returns branch", func(t *testing.T) {
		tmpDir := t.TempDir()
		runGitCmd := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = tmpDir
			out, err := cmd.CombinedOutput()
			require.NoErrorf(t, err, "git %v failed: %s", args, string(out))
		}

		runGitCmd("init")
		runGitCmd("checkout", "-b", "feat/test")
		runGitCmd("config", "user.email", "test@example.com")
		runGitCmd("config", "user.name", "Test User")
		runGitCmd("remote", "add", "origin", "https://github.com/divergedev/diverge.git")
		runGitCmd("commit", "--allow-empty", "-m", "initial commit")

		require.NoError(t, os.Chdir(tmpDir))

		branch, err := d.DetectGitBranch(ctx)
		require.NoError(t, err)
		assert.Equal(t, "feat/test", branch)
	})
}

func TestDefaultEnvironmentDetector_DetectLocalIP(t *testing.T) {
	d := &DefaultEnvironmentDetector{}
	ipStr, err := d.DetectLocalIP(context.Background())
	if err != nil {
		assert.ErrorIs(t, err, ErrTailscaleNotFound)
		return
	}
	parsed := net.ParseIP(ipStr)
	require.NotNil(t, parsed, "expected valid IP address, got %q", ipStr)
	assert.NotNil(t, parsed.To4(), "expected IPv4 address, got %q", ipStr)
}
