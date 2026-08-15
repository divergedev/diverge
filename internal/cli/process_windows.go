//go:build windows

package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
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
