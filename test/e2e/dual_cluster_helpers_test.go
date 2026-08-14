//go:build e2e_dual

package e2e

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func createK3dCluster(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "k3d", "cluster", "create", name,
		"--no-lb",
		"--k3s-arg", "--disable=traefik@server:0",
		"--wait",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "failed to create k3d cluster %s", name)

	t.Cleanup(func() {
		delCmd := exec.Command("k3d", "cluster", "delete", name)
		delCmd.Run() // best-effort cleanup
	})
}

func installCRDs(t *testing.T, contextName string) {
	t.Helper()
	// Placeholder
}

func deployEchoServer(t *testing.T, contextName string) {
	t.Helper()
	// Placeholder
}
