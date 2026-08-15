package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitCmd_DryRun(t *testing.T) {
	// Override execCommand to avoid real commands
	origExecCommand := execCommand
	defer func() { execCommand = origExecCommand }()

	execCommand = func(command string, args ...string) *exec.Cmd {
		// Just a dummy command that does nothing
		return exec.Command("true")
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
	origExecCommand := execCommand
	defer func() { execCommand = origExecCommand }()

	execCommand = func(command string, args ...string) *exec.Cmd {
		return exec.Command("true")
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
