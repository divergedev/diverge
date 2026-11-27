//go:build windows

package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"time"
)

func runChildProcess(ctx context.Context, args []string, envMap map[string]string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	env := os.Environ()
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		fmt.Printf("⚠️  Failed to start child process: %v\n", err)
		return nil, err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	go func() {
		select {
		case sig := <-sigCh:
			fmt.Printf("\nReceived signal %v, terminating child process...\n", sig)
			_ = cmd.Process.Kill()
		case <-ctx.Done():
			// Context canceled
			_ = cmd.Process.Kill()
		}
	}()

	return cmd, nil
}

// processGroupHandle manages supervised process termination on Windows.
// On Windows, supervised shutdown terminates only the direct child process
// (via cmd.Process.Kill); descendant processes spawned by the launcher are not
// tracked via Job Objects.
type processGroupHandle struct{}

// configureSupervisorCmd configures cmd for Windows supervised execution,
// where shutdown terminates only the direct child process.
func configureSupervisorCmd(cmd *exec.Cmd, _ time.Duration) *processGroupHandle {
	h := &processGroupHandle{}
	cmd.Cancel = func() error {
		return h.terminate(cmd)
	}
	return h
}

func (h *processGroupHandle) capturePID(_ *exec.Cmd) {}

func (h *processGroupHandle) terminate(cmd *exec.Cmd) error {
	if cmd != nil && cmd.Process != nil {
		return cmd.Process.Kill()
	}
	return nil
}

func (h *processGroupHandle) markReaped() bool {
	return false
}

func (h *processGroupHandle) remainingGrace(grace time.Duration) time.Duration {
	return grace
}

func (h *processGroupHandle) killGroup() {}

func (h *processGroupHandle) waitGroupExit(_ bool, _ time.Duration) {}

func signalExitCode(_ *exec.ExitError) int {
	return 1
}

func notifySupervisorSignals(sigCh chan os.Signal) {
	signal.Notify(sigCh, os.Interrupt)
}

func rootSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
