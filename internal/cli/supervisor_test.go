//go:build !windows

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// safeBuffer wraps bytes.Buffer with a mutex for race-free concurrent use.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *safeBuffer) Write(p []byte) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *safeBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}
func TestSupervisor_Start(t *testing.T) {
	envBuilder := func() map[string]string {
		return map[string]string{"TEST_VAR": "hello"}
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := NewSupervisor([]string{"echo", "started"}, envBuilder)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	// Give child time to start and exit naturally.
	time.Sleep(500 * time.Millisecond)
	cancel()

	err := <-errCh
	assert.NoError(t, err)
}

func TestSupervisor_Restart(t *testing.T) {
	var callCount atomic.Int32
	envBuilder := func() map[string]string {
		n := callCount.Add(1)
		return map[string]string{"CALL_COUNT": string(rune('0' + n))}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use sleep so child stays alive long enough for restart.
	s := NewSupervisor([]string{"sleep", "10"}, envBuilder)

	go func() {
		_ = s.Run(ctx)
	}()

	// Wait for first start.
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(1), callCount.Load())

	// Request restart.
	s.RequestRestart("test restart", map[string]string{"CALL_COUNT": "1 → 2"})

	// Wait for restart to complete.
	time.Sleep(1 * time.Second)
	assert.GreaterOrEqual(t, callCount.Load(), int32(2), "envBuilder should be called again on restart")
}

func TestSupervisor_CircuitBreaker(t *testing.T) {
	var buf safeBuffer
	envBuilder := func() map[string]string {
		return map[string]string{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// "false" exits immediately with code 1 — triggers rapid fail.
	s := NewSupervisor([]string{"false"}, envBuilder)
	s.output = &buf

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	// Send restart requests to trigger the rapid-fail loop.
	for i := 0; i < 5; i++ {
		time.Sleep(200 * time.Millisecond)
		s.RequestRestart("circuit breaker test", nil)
	}

	time.Sleep(2 * time.Second)

	s.mu.Lock()
	halted := s.halted
	s.mu.Unlock()

	assert.True(t, halted, "circuit breaker should halt after rapid failures")
	assert.Contains(t, buf.String(), "Halting auto-restart")

	cancel()
	<-errCh
}

func TestSupervisor_ChildCrashKeepsSession(t *testing.T) {
	envBuilder := func() map[string]string {
		return map[string]string{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// "false" exits with code 1 immediately.
	s := NewSupervisor([]string{"false"}, envBuilder)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	// Wait for child to crash.
	time.Sleep(500 * time.Millisecond)

	// Supervisor should still be alive — send a signal to verify.
	select {
	case <-s.done:
		t.Fatal("Supervisor should not have exited after child crash")
	default:
		// Good — still alive.
	}

	cancel()
	err := <-errCh
	assert.NoError(t, err)
}

func TestSupervisor_GracefulShutdown(t *testing.T) {
	envBuilder := func() map[string]string {
		return map[string]string{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := NewSupervisor([]string{"sleep", "60"}, envBuilder)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	// Wait for child to start.
	time.Sleep(300 * time.Millisecond)

	// Cancel context — should trigger graceful shutdown.
	cancel()

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Supervisor did not shut down within 10 seconds")
	}
}

func TestSupervisor_EnvDiffLogging(t *testing.T) {
	var buf safeBuffer

	envBuilder := func() map[string]string {
		return map[string]string{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := NewSupervisor([]string{"sleep", "10"}, envBuilder)
	s.output = &buf

	go func() {
		_ = s.Run(ctx)
	}()

	time.Sleep(300 * time.Millisecond)

	s.RequestRestart("env changed", map[string]string{
		"DIVERGE_SVC_AUTH_URL": "http://old → http://new",
	})

	time.Sleep(1 * time.Second)
	cancel()

	time.Sleep(200 * time.Millisecond)

	output := buf.String()
	assert.Contains(t, output, "DIVERGE_SVC_AUTH_URL")
	assert.Contains(t, output, "env changed")
}

func TestSupervisor_ExitOnChildExit(t *testing.T) {
	t.Run("zero exit code returns nil", func(t *testing.T) {
		var outBuf, stdoutBuf, stderrBuf safeBuffer
		var gotCode atomic.Int32
		gotCode.Store(-1)

		s := NewSupervisorWithOptions(
			[]string{"sh", "-c", "echo hi; echo err >&2"},
			nil,
			WithSupervisorOutput(&outBuf),
			WithSupervisorChildIO(&stdoutBuf, &stderrBuf),
			WithExitOnChildExit(true),
			WithOnChildExit(func(code int, _ error) {
				gotCode.Store(int32(code))
			}),
		)
		err := s.Run(t.Context())
		require.NoError(t, err)
		assert.Equal(t, int32(0), gotCode.Load())
		assert.Equal(t, "hi\n", stdoutBuf.String())
		assert.Equal(t, "err\n", stderrBuf.String())
		assert.Empty(t, outBuf.String())
	})

	t.Run("non-zero exit code returns ChildExitError", func(t *testing.T) {
		var gotCode atomic.Int32
		s := NewSupervisorWithOptions(
			[]string{"sh", "-c", "exit 42"},
			nil,
			WithExitOnChildExit(true),
			WithOnChildExit(func(code int, _ error) {
				gotCode.Store(int32(code))
			}),
		)
		err := s.Run(t.Context())
		require.Error(t, err)
		var childExit *ChildExitError
		require.True(t, errors.As(err, &childExit))
		assert.Equal(t, 42, childExit.Code)
		assert.Equal(t, int32(42), gotCode.Load())
	})

	t.Run("signal termination returns 128 + signal", func(t *testing.T) {
		var gotCode atomic.Int32
		s := NewSupervisorWithOptions(
			[]string{"sh", "-c", "kill -TERM $$"},
			nil,
			WithExitOnChildExit(true),
			WithOnChildExit(func(code int, _ error) {
				gotCode.Store(int32(code))
			}),
		)
		err := s.Run(t.Context())
		require.Error(t, err)
		var childExit *ChildExitError
		require.True(t, errors.As(err, &childExit))
		wantCode := 128 + int(syscall.SIGTERM)
		assert.Equal(t, wantCode, childExit.Code)
		assert.Equal(t, int32(wantCode), gotCode.Load())
	})
}

func readWorkerPIDFile(t *testing.T, pidFile string) int {
	t.Helper()
	var pid int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || parsed <= 0 {
			return false
		}
		pid = parsed
		return true
	}, 5*time.Second, 20*time.Millisecond, "timed out waiting for worker pid file %s", pidFile)
	t.Cleanup(func() {
		if pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return pid
}

const stubbornWorkerLauncherScript = `
trap 'exit 0' TERM
sh -c 'trap "" TERM; echo $$ > "$WORKER_PID_FILE.tmp" && mv "$WORKER_PID_FILE.tmp" "$WORKER_PID_FILE"; exec sleep 60' >/dev/null 2>&1 &
wait
`

func isProcessTerminated(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return true
	}
	out, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output() // #nosec G204 -- pid is an integer formatted with strconv.Itoa
	if err != nil {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}
	state := strings.TrimSpace(string(out))
	return state == "" || strings.HasPrefix(state, "Z")
}

func TestSupervisor_KillsStubbornGrandchildWhenLauncherExitsOnSIGTERM(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "worker.pid")
	envBuilder := func() map[string]string {
		return map[string]string{
			"WORKER_PID_FILE": pidFile,
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var outBuf safeBuffer
	s := NewSupervisorWithOptions(
		[]string{"sh", "-c", stubbornWorkerLauncherScript},
		envBuilder,
		WithSupervisorOutput(&outBuf),
		WithSupervisorChildIO(&outBuf, &outBuf),
	)
	s.gracefulTimeout = 200 * time.Millisecond

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	workerPID := readWorkerPIDFile(t, pidFile)

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not exit within 3s after canceling session with stubborn worker")
	}

	assert.Eventually(t, func() bool {
		return isProcessTerminated(workerPID)
	}, 500*time.Millisecond, 20*time.Millisecond, "stubborn grandchild worker pid %d should be terminated or zombie", workerPID)
}

func TestSupervisor_RestartKillsStubbornGrandchildBeforeRespawn(t *testing.T) {
	pidFile1 := filepath.Join(t.TempDir(), "worker1.pid")
	pidFile2 := filepath.Join(t.TempDir(), "worker2.pid")

	var worker1PID atomic.Int64
	var worker1AliveBeforeRespawn atomic.Bool
	var calls atomic.Int32
	var outBuf safeBuffer

	envBuilder := func() map[string]string {
		call := calls.Add(1)
		if call == 1 {
			return map[string]string{"WORKER_PID_FILE": pidFile1}
		}
		if call == 2 {
			if pid := int(worker1PID.Load()); pid > 0 {
				if !isProcessTerminated(pid) {
					worker1AliveBeforeRespawn.Store(true)
				}
			}
		}
		return map[string]string{"WORKER_PID_FILE": pidFile2}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := NewSupervisorWithOptions(
		[]string{"sh", "-c", stubbornWorkerLauncherScript},
		envBuilder,
		WithSupervisorOutput(&outBuf),
		WithSupervisorChildIO(&outBuf, &outBuf),
	)
	s.gracefulTimeout = 200 * time.Millisecond

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	pid1 := readWorkerPIDFile(t, pidFile1)
	worker1PID.Store(int64(pid1))

	s.RequestRestart("env changed", map[string]string{"FOO": "bar"})

	_ = readWorkerPIDFile(t, pidFile2)
	assert.False(t, worker1AliveBeforeRespawn.Load(), "worker #1 should be terminated before replacement starts")
	assert.True(t, isProcessTerminated(pid1), "worker #1 pid %d should be terminated or zombie", pid1)

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not shut down within 3s")
	}
}

func TestSupervisor_NaturalLauncherExitCleansUpGrandchildAndPreservesExitCode(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "natural-worker.pid")
	var outBuf safeBuffer

	script := `
sh -c 'trap "" TERM; echo $$ > "$WORKER_PID_FILE.tmp" && mv "$WORKER_PID_FILE.tmp" "$WORKER_PID_FILE"; exec sleep 60' >/dev/null 2>&1 &
while [ ! -f "$WORKER_PID_FILE" ]; do sleep 0.02; done
exit 42
`
	s := NewSupervisorWithOptions(
		[]string{"sh", "-c", script},
		func() map[string]string { return map[string]string{"WORKER_PID_FILE": pidFile} },
		WithExitOnChildExit(true),
		WithSupervisorOutput(&outBuf),
		WithSupervisorChildIO(&outBuf, &outBuf),
	)
	s.gracefulTimeout = 200 * time.Millisecond

	err := s.Run(t.Context())
	require.Error(t, err)
	var childExit *ChildExitError
	require.True(t, errors.As(err, &childExit))
	assert.Equal(t, 42, childExit.Code)

	workerPID := readWorkerPIDFile(t, pidFile)
	assert.Eventually(t, func() bool {
		return isProcessTerminated(workerPID)
	}, 500*time.Millisecond, 20*time.Millisecond, "stubborn worker pid %d should be cleaned up after natural launcher exit", workerPID)
}

func TestSupervisor_PromptShutdownWhenGroupExitsOnSIGTERM(t *testing.T) {
	readyFile := filepath.Join(t.TempDir(), "ready")
	var outBuf safeBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := NewSupervisorWithOptions(
		[]string{"sh", "-c", "touch \"$READY_FILE\"; exec sleep 60"},
		func() map[string]string { return map[string]string{"READY_FILE": readyFile} },
		WithSupervisorOutput(&outBuf),
		WithSupervisorChildIO(&outBuf, &outBuf),
	)
	s.gracefulTimeout = 5 * time.Second

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	require.Eventually(t, func() bool {
		_, err := os.Stat(readyFile)
		return err == nil
	}, 5*time.Second, 20*time.Millisecond)

	start := time.Now()
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(1 * time.Second):
		t.Fatal("supervisor waited too long when process group exited promptly on SIGTERM")
	}
	assert.Less(t, time.Since(start), 1*time.Second)
}
