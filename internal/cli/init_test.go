package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/divergedev/diverge/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
)

func TestInitCmd_DryRun(t *testing.T) {
	// Override execCommandContext, lookPath, and localChartExists to avoid real commands/checks
	origExecCommandContext := execCommandContext
	origLookPath := lookPath
	origLocalChartExists := localChartExists
	defer func() {
		execCommandContext = origExecCommandContext
		lookPath = origLookPath
		localChartExists = origLocalChartExists
	}()

	lookPath = func(file string) (string, error) {
		return "/usr/local/bin/" + file, nil
	}
	localChartExists = func() bool {
		return true
	}
	execCommandContext = func(ctx context.Context, command string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "true")
	}

	app := &App{}
	cmd := newInitCmd(app)
	cmd.SetArgs([]string{"--dry-run"})

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	err := cmd.Execute()

	_ = w.Close()
	os.Stderr = oldStderr
	var outBuf bytes.Buffer
	_, _ = outBuf.ReadFrom(r)

	require.NoError(t, err)

	out := outBuf.String()
	assert.Contains(t, out, "(dry-run) k3d cluster create diverge-dev")
	assert.Contains(t, out, "(dry-run) helm install eg oci://docker.io/envoyproxy/gateway-helm")
	assert.Contains(t, out, "(dry-run) helm install diverge charts/diverge/")
	assert.Contains(t, out, "✅ Diverge playground ready!")

	// Outside a repo checkout, dry-run must preview the OCI chart ref
	localChartExists = func() bool {
		return false
	}
	cmdOCI := newInitCmd(app)
	cmdOCI.SetArgs([]string{"--dry-run"})
	r2, w2, _ := os.Pipe()
	os.Stderr = w2
	err = cmdOCI.Execute()
	_ = w2.Close()
	os.Stderr = oldStderr
	outBuf.Reset()
	_, _ = outBuf.ReadFrom(r2)
	require.NoError(t, err)
	assert.Contains(t, outBuf.String(), "(dry-run) helm install diverge oci://ghcr.io/divergedev/charts/diverge")
}

func TestInitCmd_MissingPrerequisites(t *testing.T) {
	origLookPath := lookPath
	defer func() { lookPath = origLookPath }()

	lookPath = func(file string) (string, error) {
		if file == "helm" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + file, nil
	}

	app := &App{}
	cmd := newInitCmd(app)
	cmd.SetArgs([]string{"--dry-run"})

	// capture stderr
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	err := cmd.Execute()

	_ = w.Close()
	os.Stderr = oldStderr
	var outBuf bytes.Buffer
	_, _ = outBuf.ReadFrom(r)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing prerequisites: helm")
}

func TestInitCmd_Flags(t *testing.T) {
	// Test without gateway and sample app
	origExecCommandContext := execCommandContext
	origLookPath := lookPath
	defer func() {
		execCommandContext = origExecCommandContext
		lookPath = origLookPath
	}()

	lookPath = func(file string) (string, error) {
		return "/usr/local/bin/" + file, nil
	}
	execCommandContext = func(ctx context.Context, command string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "true")
	}

	app := &App{}
	cmd := newInitCmd(app)
	cmd.SetArgs([]string{"--dry-run", "--install-gateway=false", "--install-sample-app=false", "--cluster-name=test-cluster"})

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	err := cmd.Execute()

	_ = w.Close()
	os.Stderr = oldStderr
	var outBuf bytes.Buffer
	_, _ = outBuf.ReadFrom(r)

	require.NoError(t, err)
	out := outBuf.String()

	assert.Contains(t, out, "k3d cluster create test-cluster")
	assert.NotContains(t, out, "Installing Envoy Gateway")
	assert.NotContains(t, out, "Deploying sample app")
}

func TestInitCmd_ProviderKindAndExisting(t *testing.T) {
	origExecCommandContext := execCommandContext
	origLookPath := lookPath
	defer func() {
		execCommandContext = origExecCommandContext
		lookPath = origLookPath
	}()

	lookPath = func(file string) (string, error) {
		return "/usr/local/bin/" + file, nil
	}
	execCommandContext = func(ctx context.Context, command string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "true")
	}

	// 1. Provider = kind
	app := &App{}
	cmd := newInitCmd(app)
	cmd.SetArgs([]string{"--dry-run", "--provider=kind", "--cluster-name=my-kind"})

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	err := cmd.Execute()
	_ = w.Close()
	os.Stderr = oldStderr
	var outBuf bytes.Buffer
	_, _ = outBuf.ReadFrom(r)

	require.NoError(t, err)
	assert.Contains(t, outBuf.String(), "(dry-run) kind create cluster --name my-kind --wait 60s")
	assert.Contains(t, outBuf.String(), "Cluster:  kind-my-kind")

	// 2. Provider = existing
	cmd2 := newInitCmd(app)
	cmd2.SetArgs([]string{"--dry-run", "--provider=existing"})

	r2, w2, _ := os.Pipe()
	os.Stderr = w2
	err = cmd2.Execute()
	_ = w2.Close()
	os.Stderr = oldStderr
	outBuf.Reset()
	_, _ = outBuf.ReadFrom(r2)

	require.NoError(t, err)
	assert.Contains(t, outBuf.String(), "Skipping cluster creation (provider=existing)")
	assert.Contains(t, outBuf.String(), "Cluster:  existing (current context)")
}

func TestInitCmd_OCIChartFallbackOutsideRepo(t *testing.T) {
	origExecCommandContext := execCommandContext
	origLookPath := lookPath
	origLocalChartExists := localChartExists
	defer func() {
		execCommandContext = origExecCommandContext
		lookPath = origLookPath
		localChartExists = origLocalChartExists
	}()

	lookPath = func(file string) (string, error) {
		return "/usr/local/bin/" + file, nil
	}
	localChartExists = func() bool {
		return false
	}

	var executed []string
	execCommandContext = func(ctx context.Context, command string, args ...string) *exec.Cmd {
		executed = append(executed, command+" "+bytes.NewBufferString(joinArgs(args)).String())
		return exec.CommandContext(ctx, "true")
	}

	app := &App{}
	cmd := newInitCmd(app)
	cmd.SetArgs([]string{"--provider=existing", "--install-gateway=false", "--install-sample-app=false"})

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	err := cmd.Execute()
	_ = w.Close()
	os.Stderr = oldStderr
	var outBuf bytes.Buffer
	_, _ = outBuf.ReadFrom(r)

	require.NoError(t, err)
	assert.Contains(t, executed, "helm upgrade --install diverge oci://ghcr.io/divergedev/charts/diverge -n diverge-system --create-namespace --wait")
	assert.NotContains(t, executed, "kubectl apply -f config/crd/bases/")
}

func TestInitCmd_ExistingClusterContextSelection(t *testing.T) {
	origExecCommandContext := execCommandContext
	origLookPath := lookPath
	origLocalChartExists := localChartExists
	defer func() {
		execCommandContext = origExecCommandContext
		lookPath = origLookPath
		localChartExists = origLocalChartExists
	}()

	lookPath = func(file string) (string, error) {
		return "/usr/local/bin/" + file, nil
	}
	localChartExists = func() bool {
		return false
	}

	var executed []string
	execCommandContext = func(ctx context.Context, command string, args ...string) *exec.Cmd {
		cmdLine := command + " " + joinArgs(args)
		executed = append(executed, cmdLine)
		if command == "kind" && len(args) >= 2 && args[0] == "get" && args[1] == "clusters" {
			return exec.CommandContext(ctx, "printf", "my-kind\n")
		}
		return exec.CommandContext(ctx, "true")
	}

	// 1. Existing kind cluster selects kind-my-kind context and passes --context / --kube-context
	app := &App{}
	cmd := newInitCmd(app)
	cmd.SetArgs([]string{"--provider=kind", "--cluster-name=my-kind", "--install-gateway=false", "--install-sample-app=false"})

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	err := cmd.Execute()
	_ = w.Close()
	os.Stderr = oldStderr
	var outBuf bytes.Buffer
	_, _ = outBuf.ReadFrom(r)
	require.NoError(t, err)

	assert.Contains(t, executed, "kubectl config use-context kind-my-kind")
	assert.Contains(t, executed, "helm upgrade --install diverge oci://ghcr.io/divergedev/charts/diverge -n diverge-system --create-namespace --wait --kube-context kind-my-kind")

	// 2. provider=existing honors root App.Context
	executed = nil
	appWithCtx := &App{Context: "staging-cluster"}
	cmd2 := newInitCmd(appWithCtx)
	cmd2.SetArgs([]string{"--provider=existing", "--install-gateway=false", "--install-sample-app=false"})

	r2, w2, _ := os.Pipe()
	os.Stderr = w2
	err = cmd2.Execute()
	_ = w2.Close()
	os.Stderr = oldStderr
	outBuf.Reset()
	_, _ = outBuf.ReadFrom(r2)
	require.NoError(t, err)

	assert.Contains(t, executed, "kubectl config use-context staging-cluster")
	assert.Contains(t, executed, "helm upgrade --install diverge oci://ghcr.io/divergedev/charts/diverge -n diverge-system --create-namespace --wait --kube-context staging-cluster")
	assert.Contains(t, outBuf.String(), "Cluster:  existing (staging-cluster)")
}

func joinArgs(args []string) string {
	var b bytes.Buffer
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(a)
	}
	return b.String()
}

func TestInitCmd_ConfigScaffolding(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(tmpDir+"/services/payments", 0755))
	require.NoError(t, os.MkdirAll(tmpDir+"/charts/payments", 0755))

	outFile := tmpDir + "/.diverge.yaml"
	app := &App{}
	cmd := newInitCmd(app)
	cmd.SetArgs([]string{"--config", "--output", outFile})

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	err := cmd.Execute()
	_ = w.Close()
	os.Stderr = oldStderr
	var outBuf bytes.Buffer
	_, _ = outBuf.ReadFrom(r)

	require.NoError(t, err)
	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "# yaml-language-server: $schema=https://divergedev.com/schemas/diverge.schema.json")
	assert.Contains(t, content, "version: \"1\"")
	assert.Contains(t, content, "payments:")
	assert.Contains(t, content, "services/payments/**")
	assert.Contains(t, content, "charts/payments")

	// Running again without --force should fail
	cmd2 := newInitCmd(app)
	cmd2.SetArgs([]string{"--config", "--output", outFile})
	err = cmd2.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// Validate the generated config with diverge validate
	valCmd := newValidateCmd(app)
	var valOut bytes.Buffer
	valCmd.SetOut(&valOut)
	valCmd.SetErr(&valOut)
	valCmd.SetArgs([]string{"-c", outFile})
	require.NoError(t, valCmd.Execute())
}

func TestInitCmd_ConfigScaffolding_SpecialCharsAndGitProtection(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "services", "foo: bar"), 0o755))

	outFile := filepath.Join(tmpDir, ".diverge.yaml")
	require.NoError(t, runInitConfig(outFile, false))

	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	cfg, err := config.Parse(data)
	require.NoError(t, err)
	require.Contains(t, cfg.Services, "foo: bar")

	// 1. Direct .git path must be rejected even with --force
	gitDir := filepath.Join(tmpDir, ".git")
	require.NoError(t, os.MkdirAll(gitDir, 0o755))
	gitConfig := filepath.Join(gitDir, "config")
	require.NoError(t, os.WriteFile(gitConfig, []byte("[core]\n"), 0o644))

	err = runInitConfig(gitConfig, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to write configuration")
	unchanged, err := os.ReadFile(gitConfig)
	require.NoError(t, err)
	assert.Equal(t, "[core]\n", string(unchanged))

	// 2. Symlink pointing into .git/config must be rejected even with --force
	symlinkPath := filepath.Join(tmpDir, "symlink-diverge.yaml")
	require.NoError(t, os.Symlink(gitConfig, symlinkPath))
	err = runInitConfig(symlinkPath, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to write configuration")
	unchanged, err = os.ReadFile(gitConfig)
	require.NoError(t, err)
	assert.Equal(t, "[core]\n", string(unchanged))

	// 3. Linked-worktree .git file pointing to external gitdir must protect the worktree gitdir
	wtRepo := t.TempDir()
	wtGitDir := filepath.Join(t.TempDir(), "worktrees", "wt1")
	require.NoError(t, os.MkdirAll(wtGitDir, 0o755))
	wtHead := filepath.Join(wtGitDir, "HEAD")
	require.NoError(t, os.WriteFile(wtHead, []byte("ref: refs/heads/main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(wtRepo, ".git"), []byte("gitdir: "+wtGitDir+"\n"), 0o644))

	wtSymlink := filepath.Join(wtRepo, ".diverge.yaml")
	require.NoError(t, os.Symlink(wtHead, wtSymlink))
	err = runInitConfig(wtSymlink, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to write configuration")
	unchangedHead, err := os.ReadFile(wtHead)
	require.NoError(t, err)
	assert.Equal(t, "ref: refs/heads/main\n", string(unchangedHead))
}

func TestInitConfig_ArbitraryServiceNamesProduceValidConfig_PBT(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		svcName := hegel.Draw(ht, hegel.FromRegex(`^[a-zA-Z0-9][a-zA-Z0-9 _:-]{0,18}[a-zA-Z0-9]$`, true))
		tmpDir := t.TempDir()
		require.NoError(ht, os.MkdirAll(filepath.Join(tmpDir, "services", svcName), 0o755))

		outFile := filepath.Join(tmpDir, ".diverge.yaml")
		require.NoError(ht, runInitConfig(outFile, true))

		raw, err := os.ReadFile(outFile)
		require.NoError(ht, err)

		cfg, err := config.Parse(raw)
		require.NoError(ht, err)
		require.NoError(ht, cfg.Validate())
		require.Contains(ht, cfg.Services, svcName)
	})
}

func TestInitCmd_ConfigScaffolding_StackAutoDetection(t *testing.T) {
	tmpDir := t.TempDir()
	pkgJSON := `{
	"name": "checkout-service",
	"dependencies": {
		"pg": "^8.11.3",
		"ioredis": "^5.3.2",
		"@aws-sdk/client-s3": "^3.500.0"
	}
}`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0o644))

	outFile := filepath.Join(tmpDir, ".diverge.yaml")
	app := &App{}
	cmd := newInitCmd(app)
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"--config", "--output", outFile})

	require.NoError(t, cmd.Execute())
	assert.Contains(t, errBuf.String(), "Auto-detected stack features: database=schema, async=[redis s3]")

	raw, err := os.ReadFile(outFile)
	require.NoError(t, err)
	content := string(raw)
	assert.Contains(t, content, "mode: schema")
	assert.Contains(t, content, "seed_source: public")
	assert.Contains(t, content, "protocol: redis")
	assert.Contains(t, content, "protocol: s3")

	cfg, err := config.Parse(raw)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())
	assert.Equal(t, "schema", cfg.Defaults.Database.Mode)
	assert.Equal(t, "public", cfg.Defaults.Database.SeedSource)

	svc, ok := cfg.Services["api"]
	require.True(t, ok)
	require.Len(t, svc.AsyncRoutes, 2)
	assert.Equal(t, "redis", svc.AsyncRoutes[0].Protocol)
	assert.Equal(t, "cache", svc.AsyncRoutes[0].Target)
	assert.Equal(t, "s3", svc.AsyncRoutes[1].Protocol)
	assert.Equal(t, "uploads", svc.AsyncRoutes[1].Target)
}

func TestInitCmd_ConfigScaffolding_MultiServiceAsyncRoutesScopedPerService(t *testing.T) {
	tmpDir := t.TempDir()
	apiDir := filepath.Join(tmpDir, "services", "api")
	paymentsDir := filepath.Join(tmpDir, "services", "payments")
	require.NoError(t, os.MkdirAll(apiDir, 0o755))
	require.NoError(t, os.MkdirAll(paymentsDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(apiDir, "package.json"), []byte(`{"name":"api","dependencies":{"express":"^4.18.0"}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(paymentsDir, "package.json"), []byte(`{"name":"payments","dependencies":{"ioredis":"^5.3.2"}}`), 0o644))

	outFile := filepath.Join(tmpDir, ".diverge.yaml")
	require.NoError(t, runInitConfig(outFile, false))

	raw, err := os.ReadFile(outFile)
	require.NoError(t, err)
	cfg, err := config.Parse(raw)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	apiSvc, ok := cfg.Services["api"]
	require.True(t, ok)
	assert.Empty(t, apiSvc.AsyncRoutes, "api service should not inherit payments' redis route")

	paymentsSvc, ok := cfg.Services["payments"]
	require.True(t, ok)
	require.Len(t, paymentsSvc.AsyncRoutes, 1)
	assert.Equal(t, "redis", paymentsSvc.AsyncRoutes[0].Protocol)
	assert.Equal(t, "cache", paymentsSvc.AsyncRoutes[0].Target)
}

func TestInitCmd_ConfigScaffolding_PrismaProviderPostgresDetection(t *testing.T) {
	t.Run("sqlite prisma schema does not enable postgres schema mode", func(t *testing.T) {
		tmpDir := t.TempDir()
		prismaDir := filepath.Join(tmpDir, "prisma")
		require.NoError(t, os.MkdirAll(prismaDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(prismaDir, "schema.prisma"), []byte(`
datasource db {
	provider = "sqlite"
	url      = "file:./dev.db"
}
`), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(`{"dependencies":{"prisma":"^5.0.0","drizzle-orm":"^0.30.0"}}`), 0o644))

		outFile := filepath.Join(tmpDir, ".diverge.yaml")
		require.NoError(t, runInitConfig(outFile, false))

		raw, err := os.ReadFile(outFile)
		require.NoError(t, err)
		cfg, err := config.Parse(raw)
		require.NoError(t, err)
		assert.Empty(t, cfg.Defaults.Database.Mode)
	})

	t.Run("postgresql prisma schema enables postgres schema mode", func(t *testing.T) {
		tmpDir := t.TempDir()
		prismaDir := filepath.Join(tmpDir, "prisma")
		require.NoError(t, os.MkdirAll(prismaDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(prismaDir, "schema.prisma"), []byte(`
datasource db {
	provider = "postgresql"
	url      = env("DATABASE_URL")
}
`), 0o644))

		outFile := filepath.Join(tmpDir, ".diverge.yaml")
		require.NoError(t, runInitConfig(outFile, false))

		raw, err := os.ReadFile(outFile)
		require.NoError(t, err)
		cfg, err := config.Parse(raw)
		require.NoError(t, err)
		assert.Equal(t, "schema", cfg.Defaults.Database.Mode)
		assert.Equal(t, "public", cfg.Defaults.Database.SeedSource)
	})
}
