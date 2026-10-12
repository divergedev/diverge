package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitCmd_DryRun(t *testing.T) {
	// Override execCommandContext and lookPath to avoid real commands/checks
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
		// Just a dummy command that does nothing
		return exec.CommandContext(ctx, "true")
	}

	app := &App{}
	cmd := newInitCmd(app)
	cmd.SetArgs([]string{"--dry-run"})

	// We need to capture stderr because runInit prints to os.Stderr
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
