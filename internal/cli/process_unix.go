//go:build !windows

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

func runChildProcess(ctx context.Context, args []string, envMap map[string]string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Cancel = func() error {
		// Signal entire process group
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
			// Process may have already exited
			if errors.Is(err, syscall.ESRCH) {
				return nil
			}
			return err
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case sig := <-sigCh:
			fmt.Printf("\nReceived signal %v, terminating child process tree...\n", sig)
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)

			// Wait 5 seconds, then SIGKILL if it hasn't exited
			time.AfterFunc(5*time.Second, func() {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			})
		case <-ctx.Done():
			// cmd.Cancel handles this automatically now
		}
	}()

	return cmd, nil
}
