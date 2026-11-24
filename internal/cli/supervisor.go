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

// ChildExitError reports a non-zero exit from a supervised child process so the
// CLI root command can exit with the child's status code.
type ChildExitError struct {
	Code int
	Err  error
}

// Error implements error.
func (e *ChildExitError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return fmt.Sprintf("child process exited with code %d: %v", e.Code, e.Err)
	}
	return fmt.Sprintf("child process exited with code %d", e.Code)
}

// Unwrap returns the underlying process error.
func (e *ChildExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// SupervisorOption configures optional Supervisor behavior.
type SupervisorOption func(*Supervisor)

// WithSupervisorOutput directs supervisor status and restart banners to w.
func WithSupervisorOutput(w io.Writer) SupervisorOption {
	return func(s *Supervisor) {
		if w != nil {
			s.output = w
		}
	}
}

// WithSupervisorChildIO directs child process stdout and stderr to the supplied writers.
func WithSupervisorChildIO(stdout, stderr io.Writer) SupervisorOption {
	return func(s *Supervisor) {
		if stdout != nil {
			s.childStdout = stdout
		}
		if stderr != nil {
			s.childStderr = stderr
		}
	}
}

// WithExitOnChildExit causes Run to return immediately when the child process exits
// instead of blocking indefinitely for config-change restarts.
func WithExitOnChildExit(exit bool) SupervisorOption {
	return func(s *Supervisor) {
		s.exitOnChildExit = exit
	}
}

// WithOnChildExit registers a callback invoked when the supervised child exits on its own.
func WithOnChildExit(fn func(exitCode int, err error)) SupervisorOption {
	return func(s *Supervisor) {
		s.onChildExit = fn
	}
}

// Supervisor manages the lifecycle of a child process, supporting safe restarts
// when environment configuration changes. It owns the signal loop and ensures
// proper process group reaping between restarts.
type Supervisor struct {
	args            []string
	envBuilder      func() map[string]string
	output          io.Writer // where to write log messages (default: os.Stdout)
	childStdout     io.Writer
	childStderr     io.Writer
	exitOnChildExit bool
	onChildExit     func(exitCode int, err error)
	gracefulTimeout time.Duration

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
	return NewSupervisorWithOptions(args, envBuilder)
}

// NewSupervisorWithOptions creates a supervisor with functional options.
func NewSupervisorWithOptions(args []string, envBuilder func() map[string]string, opts ...SupervisorOption) *Supervisor {
	s := &Supervisor{
		args:            args,
		envBuilder:      envBuilder,
		output:          os.Stdout,
		childStdout:     os.Stdout,
		childStderr:     os.Stderr,
		gracefulTimeout: gracefulTimeout,
		done:            make(chan struct{}),
		restartCh:       make(chan restartRequest, 1), // buffered to avoid blocking ConfigWatcher
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

func (s *Supervisor) graceTimeout() time.Duration {
	if s.gracefulTimeout > 0 {
		return s.gracefulTimeout
	}
	return gracefulTimeout
}

type waitResult struct {
	err        error
	groupAlive bool
}

// Run starts the child process and blocks until ctx is cancelled.
// It handles restart requests and parent signals (SIGINT/SIGTERM).
func (s *Supervisor) Run(ctx context.Context) error {
	// Own the parent signal loop — no leaking signal.Notify goroutines.
	sigCh := make(chan os.Signal, 1)
	notifySupervisorSignals(sigCh)
	defer signal.Stop(sigCh)
	defer close(s.done)

	for {
		startTime := time.Now()
		cmd, procHandle, err := s.startChild(ctx)
		if err != nil {
			return fmt.Errorf("failed to start child process: %w", err)
		}

		// Wait for child exit, parent signal, restart request, or context cancel.
		waitCh := make(chan waitResult, 1)
		go func() {
			err := cmd.Wait()
			groupAlive := procHandle.markReaped()
			waitCh <- waitResult{err: err, groupAlive: groupAlive}
		}()

		select {
		case res := <-waitCh:
			err := res.err
			if res.groupAlive {
				_ = procHandle.terminate(cmd)
				procHandle.waitGroupExit(true, s.graceTimeout())
			}
			// Child exited on its own — record for circuit breaker.
			s.RecordChildExit(startTime)
			s.mu.Lock()
			s.cmd = nil
			s.cancel = nil
			s.mu.Unlock()

			if ctx.Err() != nil {
				return nil // parent shutting down
			}

			exitCode := 0
			if err != nil {
				exitCode = 1
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					exitCode = exitErr.ExitCode()
					if exitCode < 0 {
						exitCode = signalExitCode(exitErr)
					}
				}
			}
			if s.onChildExit != nil {
				s.onChildExit(exitCode, err)
			}
			if s.exitOnChildExit {
				if err != nil {
					return &ChildExitError{Code: exitCode, Err: err}
				}
				return nil
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
			s.stopAndWaitChild(cmd, procHandle, waitCh)
			return nil

		case <-ctx.Done():
			s.stopAndWaitChild(cmd, procHandle, waitCh)
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
			// Wait for child process group to fully exit before respawning (prevent EADDRINUSE).
			s.stopAndWaitChild(cmd, procHandle, waitCh)
			s.RecordChildExit(startTime)
			s.mu.Lock()
			s.cmd = nil
			s.cancel = nil
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
func (s *Supervisor) startChild(ctx context.Context) (*exec.Cmd, *processGroupHandle, error) {
	var envMap map[string]string
	if s.envBuilder != nil {
		envMap = s.envBuilder()
	}

	childCtx, childCancel := context.WithCancel(ctx)
	grace := s.graceTimeout()

	cmd := exec.CommandContext(childCtx, s.args[0], s.args[1:]...) // #nosec G204 -- user-provided local dev command
	cmd.Stdin = os.Stdin
	cmd.Stdout = s.childStdout
	cmd.Stderr = s.childStderr
	procHandle := configureSupervisorCmd(cmd, grace)

	// Build clean env — no stale accumulation.
	env := os.Environ()
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		childCancel()
		return nil, nil, err
	}
	procHandle.capturePID(cmd)

	s.mu.Lock()
	s.cmd = cmd
	s.cancel = childCancel
	s.mu.Unlock()

	return cmd, procHandle, nil
}

func (s *Supervisor) stopAndWaitChild(cmd *exec.Cmd, procHandle *processGroupHandle, waitCh <-chan waitResult) {
	select {
	case res := <-waitCh:
		if res.groupAlive {
			_ = procHandle.terminate(cmd)
			procHandle.waitGroupExit(true, s.graceTimeout())
		}
		s.stopChild()
		return
	default:
	}

	_ = procHandle.terminate(cmd)
	s.stopChild()

	timer := time.NewTimer(procHandle.remainingGrace(s.graceTimeout()))
	defer timer.Stop()

	select {
	case res := <-waitCh:
		if res.groupAlive {
			procHandle.waitGroupExit(true, s.graceTimeout())
		}
	case <-timer.C:
		procHandle.killGroup()
		res := <-waitCh
		if res.groupAlive {
			procHandle.waitGroupExit(false, 0)
		}
	}
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
