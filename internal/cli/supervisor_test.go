//go:build !windows

package cli

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
