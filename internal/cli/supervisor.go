//go:build !windows

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"
)

const (
	// rapidFailThreshold is how fast a child must exit to count as a rapid failure.
	rapidFailThreshold = 3 * time.Second
	// maxRapidFails is the circuit breaker limit.
	maxRapidFails = 3
	// gracefulTimeout is how long to wait for SIGTERM before SIGKILL.
	gracefulTimeout = 5 * time.Second
)

// Supervisor manages the lifecycle of a child process, supporting safe restarts
// when environment configuration changes. It owns the signal loop and ensures
// proper process group reaping between restarts.
type Supervisor struct {
	args       []string
	envBuilder func() map[string]string
	output     io.Writer // where to write log messages (default: os.Stdout)

	mu         sync.Mutex
	cmd        *exec.Cmd
	cancel     context.CancelFunc // cancel for child context
	restarts   int
	rapidFails int
	halted     bool

	// done is closed when the supervisor loop exits.
	done chan struct{}
	// restartCh receives restart requests with a reason and env diff.
	restartCh chan restartRequest
}

type restartRequest struct {
	Reason  string
	EnvDiff map[string]string // key → "old → new" or "new_value" for additions
}

// NewSupervisor creates a supervisor for the given command args.
// envBuilder is called fresh on each (re)start to produce a clean env map.
func NewSupervisor(args []string, envBuilder func() map[string]string) *Supervisor {
	return &Supervisor{
		args:       args,
		envBuilder: envBuilder,
		output:     os.Stdout,
		done:       make(chan struct{}),
		restartCh:  make(chan restartRequest, 1), // buffered to avoid blocking ConfigWatcher
	}
}

// Run starts the child process and blocks until ctx is cancelled.
// It handles restart requests and parent signals (SIGINT/SIGTERM).
func (s *Supervisor) Run(ctx context.Context) error {
	// Own the parent signal loop — no leaking signal.Notify goroutines.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	defer close(s.done)

	for {
		startTime := time.Now()
		if err := s.startChild(ctx); err != nil {
			return fmt.Errorf("failed to start child process: %w", err)
		}

		// Wait for child exit, parent signal, restart request, or context cancel.
		waitCh := make(chan error, 1)
		go func() {
			waitCh <- s.cmd.Wait()
		}()

		select {
		case err := <-waitCh:
			// Child exited on its own — record for circuit breaker.
			s.RecordChildExit(startTime)
			s.mu.Lock()
			s.cmd = nil
			s.mu.Unlock()

			if ctx.Err() != nil {
				return nil // parent shutting down
			}
			if err != nil {
				_, _ = fmt.Fprintf(s.output, "[diverge] ⚠️  Child process exited: %v\n", err)
			} else {
				_, _ = fmt.Fprintln(s.output, "[diverge] Child process exited normally")
			}
			// Don't tear down session — wait for restart or parent signal.
			select {
			case <-ctx.Done():
				return nil
			case <-sigCh:
				return nil
			case req := <-s.restartCh:
				s.printRestartBanner(req)
				continue
			}

		case sig := <-sigCh:
			// Parent received signal — forward to child and exit.
			_, _ = fmt.Fprintf(s.output, "\n[diverge] Received %v, shutting down...\n", sig)
			s.stopChild()
			<-waitCh // reap child
			return nil

		case <-ctx.Done():
			s.stopChild()
			<-waitCh // reap child
			return nil

		case req := <-s.restartCh:
			// Config changed — restart child.
			s.mu.Lock()
			halted := s.halted
			s.mu.Unlock()
			if halted {
				continue
			}
			s.printRestartBanner(req)
			s.stopChild()
			// Wait for child to fully exit before respawning (prevent EADDRINUSE).
			<-waitCh
			s.RecordChildExit(startTime)
			s.mu.Lock()
			s.cmd = nil
			cbHalted := s.halted
			s.mu.Unlock()

			if cbHalted {
				// Circuit breaker tripped during this restart cycle.
				select {
				case <-ctx.Done():
					return nil
				case <-sigCh:
					return nil
				case <-s.restartCh:
					continue
				}
			}
			s.mu.Lock()
			s.restarts++
			s.mu.Unlock()
			continue
		}
	}
}

// RequestRestart sends a non-blocking restart request to the supervisor.
func (s *Supervisor) RequestRestart(reason string, envDiff map[string]string) {
	req := restartRequest{Reason: reason, EnvDiff: envDiff}
	select {
	case s.restartCh <- req:
	default:
		// Already a pending restart — drop this one (debounce).
	}
}

// startChild builds and starts a new child process.
func (s *Supervisor) startChild(ctx context.Context) error {
	envMap := s.envBuilder()

	childCtx, childCancel := context.WithCancel(ctx)

	cmd := exec.CommandContext(childCtx, s.args[0], s.args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.WaitDelay = gracefulTimeout

	// Build clean env — no stale accumulation.
	env := os.Environ()
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	// Cancel sends SIGTERM to the process group.
	cmd.Cancel = func() error {
		pgid := -cmd.Process.Pid
		if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}

	if err := cmd.Start(); err != nil {
		childCancel()
		return err
	}

	s.mu.Lock()
	s.cmd = cmd
	s.cancel = childCancel
	s.mu.Unlock()

	return nil
}

// stopChild sends SIGTERM to the child process group via context cancellation.
// The caller must drain waitCh to reap the child — WaitDelay handles SIGKILL escalation.
func (s *Supervisor) stopChild() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel() // triggers cmd.Cancel → SIGTERM to process group
	}
}

// RecordChildExit records a child exit for circuit breaker tracking.
// Called after cmd.Wait() returns in the Run loop.
func (s *Supervisor) RecordChildExit(startTime time.Time) {
	elapsed := time.Since(startTime)

	s.mu.Lock()
	defer s.mu.Unlock()

	if elapsed < rapidFailThreshold {
		s.rapidFails++
		if s.rapidFails >= maxRapidFails {
			s.halted = true
			_, _ = fmt.Fprintf(s.output, "[diverge] ⚠️  Child process crashed %d times in rapid succession. Halting auto-restart.\n", s.rapidFails)
			_, _ = fmt.Fprintln(s.output, "[diverge] Fix the issue and restart with: diverge dev --watch-env ...")
		}
	} else {
		s.rapidFails = 0 // reset on successful run
	}
}

func (s *Supervisor) printRestartBanner(req restartRequest) {
	s.mu.Lock()
	n := s.restarts + 1
	s.mu.Unlock()

	_, _ = fmt.Fprintln(s.output)
	_, _ = fmt.Fprintln(s.output, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	_, _ = fmt.Fprintf(s.output, "[diverge] 🔄 Restarting child process (restart #%d)\n", n)
	_, _ = fmt.Fprintf(s.output, "[diverge] Reason: %s\n", req.Reason)
	// Sort keys for deterministic output.
	keys := make([]string, 0, len(req.EnvDiff))
	for k := range req.EnvDiff {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _ = fmt.Fprintf(s.output, "[diverge]   %s: %s\n", k, req.EnvDiff[k])
	}
	_, _ = fmt.Fprintln(s.output, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
}
