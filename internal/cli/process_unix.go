//go:build !windows

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func runChildProcess(ctx context.Context, args []string, envMap map[string]string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		pgid := -cmd.Process.Pid
		if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		// Start SIGKILL fallback after WaitDelay
		go func() {
			time.Sleep(cmd.WaitDelay)
			_ = syscall.Kill(pgid, syscall.SIGKILL)
		}()
		return nil
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	env := os.Environ()
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second

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

const processGroupPollInterval = 20 * time.Millisecond

type processGroupHandle struct {
	mu        sync.Mutex
	pid       int
	termSent  bool
	termAt    time.Time
	groupGone bool
}

func configureSupervisorCmd(cmd *exec.Cmd, grace time.Duration) *processGroupHandle {
	h := &processGroupHandle{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if grace > 0 {
		cmd.WaitDelay = grace + 2*time.Second
	}
	cmd.Cancel = func() error {
		return h.terminate(cmd)
	}
	return h
}

func (h *processGroupHandle) capturePID(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	h.mu.Lock()
	h.pid = cmd.Process.Pid
	h.mu.Unlock()
}

func processGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (h *processGroupHandle) terminate(cmd *exec.Cmd) error {
	h.mu.Lock()
	if h.groupGone {
		h.mu.Unlock()
		return os.ErrProcessDone
	}
	if h.termSent {
		h.mu.Unlock()
		return nil
	}
	pid := h.pid
	if pid == 0 && cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
		h.pid = pid
	}
	if pid <= 0 {
		h.mu.Unlock()
		return nil
	}
	h.termSent = true
	h.termAt = time.Now()
	h.mu.Unlock()

	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			h.mu.Lock()
			h.groupGone = true
			h.mu.Unlock()
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}

func (h *processGroupHandle) markReaped() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.groupGone || h.pid <= 0 {
		h.groupGone = true
		return false
	}
	if !processGroupAlive(h.pid) {
		h.groupGone = true
		return false
	}
	return true
}

func (h *processGroupHandle) remainingGrace(grace time.Duration) time.Duration {
	h.mu.Lock()
	termAt := h.termAt
	h.mu.Unlock()
	if termAt.IsZero() {
		return grace
	}
	rem := time.Until(termAt.Add(grace))
	if rem < 0 {
		return 0
	}
	return rem
}

func (h *processGroupHandle) killGroup() {
	h.mu.Lock()
	if h.groupGone || h.pid <= 0 {
		h.mu.Unlock()
		return
	}
	pid := h.pid
	h.mu.Unlock()

	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func (h *processGroupHandle) pollUntilGone(pid int, until time.Time) bool {
	if !processGroupAlive(pid) {
		h.mu.Lock()
		h.groupGone = true
		h.mu.Unlock()
		return true
	}
	if !time.Now().Before(until) {
		return false
	}
	ticker := time.NewTicker(processGroupPollInterval)
	defer ticker.Stop()
	for {
		if !processGroupAlive(pid) {
			h.mu.Lock()
			h.groupGone = true
			h.mu.Unlock()
			return true
		}
		if !time.Now().Before(until) {
			return false
		}
		<-ticker.C
	}
}

func (h *processGroupHandle) waitGroupExit(escalate bool, grace time.Duration) {
	h.mu.Lock()
	if h.groupGone || h.pid <= 0 {
		h.mu.Unlock()
		return
	}
	pid := h.pid
	deadline := h.termAt.Add(grace)
	if h.termAt.IsZero() {
		deadline = time.Now().Add(grace)
	}
	h.mu.Unlock()

	if escalate {
		if h.pollUntilGone(pid, deadline) {
			return
		}
		h.killGroup()
	}
	_ = h.pollUntilGone(pid, time.Now().Add(2*time.Second))
}

func signalExitCode(exitErr *exec.ExitError) int {
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return 1
}

func notifySupervisorSignals(sigCh chan os.Signal) {
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
}

func rootSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
