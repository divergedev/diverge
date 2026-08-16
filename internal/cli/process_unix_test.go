//go:build !windows

package cli

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRunChildProcess_ProcessGroupKill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Start a process that spawns a child
	errCh := make(chan error, 1)
	go func() {
		cmd, err := runChildProcess(ctx, []string{"sh", "-c", "sleep 60 & wait"}, map[string]string{})
		if err != nil {
			errCh <- err
			return
		}
		errCh <- cmd.Wait()
	}()

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	// Cancel context - should kill process group
	cancel()

	err := <-errCh
	// Should exit with context error, not hang
	assert.Error(t, err)
}
