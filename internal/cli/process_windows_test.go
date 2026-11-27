//go:build windows

package cli

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigureSupervisorCmd_Windows(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "echo", "ok")
	handle := configureSupervisorCmd(cmd, 5*time.Second)
	require.NotNil(t, handle)
	require.NotNil(t, cmd.Cancel)

	// Safe on nil/unstarted process.
	handle.capturePID(cmd)
	assert.NoError(t, cmd.Cancel())
	assert.NoError(t, handle.terminate(nil))
	assert.False(t, handle.markReaped())
	assert.Equal(t, 250*time.Millisecond, handle.remainingGrace(250*time.Millisecond))
	handle.killGroup()
	handle.waitGroupExit(true, time.Second)
}

func TestWindowsSignalHelpers(t *testing.T) {
	assert.Equal(t, 1, signalExitCode(nil))
	assert.Equal(t, []os.Signal{os.Interrupt}, rootSignals())

	sigCh := make(chan os.Signal, 1)
	notifySupervisorSignals(sigCh)
}
