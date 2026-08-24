package server

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"connectrpc.com/connect"

	"github.com/divergedev/diverge/internal/server/auth"
)

// StreamLimiterMetrics provides decoupled metric callbacks.
// Matches the BroadcasterMetrics pattern to avoid coupling to Prometheus.
type StreamLimiterMetrics struct {
	IncActive func()
	DecActive func()
	Rejected  func(reason string) // "per_user" or "global"
}

// StreamLimiter enforces per-user and global stream concurrency limits.
// It replaces the previous global channel semaphore to prevent a single
// user from exhausting all stream slots (noisy-neighbor DoS).
type StreamLimiter struct {
	mu         sync.Mutex
	maxGlobal  int
	maxPerUser int
	global     int
	perUser    map[string]int
	metrics    StreamLimiterMetrics
}

// NewStreamLimiter creates a limiter with the given global and per-user caps.
// Panics if maxGlobal <= 0 or maxPerUser <= 0 or maxPerUser > maxGlobal.
func NewStreamLimiter(maxGlobal, maxPerUser int, metrics ...StreamLimiterMetrics) *StreamLimiter {
	if maxGlobal <= 0 {
		panic("maxGlobal must be > 0")
	}
	if maxPerUser <= 0 {
		panic("maxPerUser must be > 0")
	}
	if maxPerUser > maxGlobal {
		panic("maxPerUser must be <= maxGlobal")
	}
	var m StreamLimiterMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &StreamLimiter{
		maxGlobal:  maxGlobal,
		maxPerUser: maxPerUser,
		perUser:    make(map[string]int),
		metrics:    m,
	}
}

// Acquire reserves a stream slot for the authenticated user in ctx.
// Returns a release function that MUST be called when the stream ends.
// The release function is always non-nil and safe to call multiple times.
//
// Errors:
//   - CodeUnauthenticated: missing or anonymous user identity
//   - CodeResourceExhausted: per-user or global limit reached
func (l *StreamLimiter) Acquire(ctx context.Context) (func(), error) {
	noop := func() {}

	user, ok := auth.UserInfoFromContext(ctx)
	if !ok || user == nil || user.Username == "" {
		return noop, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated: missing user identity"))
	}

	username := user.Username

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.perUser[username] >= l.maxPerUser {
		if l.metrics.Rejected != nil {
			l.metrics.Rejected("per_user")
		}
		return noop, connect.NewError(connect.CodeResourceExhausted,
			fmt.Errorf("stream limit of %d reached for user %q; close idle watch/log streams", l.maxPerUser, username))
	}

	if l.global >= l.maxGlobal {
		if l.metrics.Rejected != nil {
			l.metrics.Rejected("global")
		}
		return noop, connect.NewError(connect.CodeResourceExhausted,
			errors.New("server stream capacity reached; try again later"))
	}

	l.global++
	l.perUser[username]++
	if l.metrics.IncActive != nil {
		l.metrics.IncActive()
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.global--
			l.perUser[username]--
			if l.perUser[username] == 0 {
				delete(l.perUser, username)
			}
			if l.metrics.DecActive != nil {
				l.metrics.DecActive()
			}
		})
	}, nil
}

// ActiveStreams returns the current global stream count (for testing/metrics).
func (l *StreamLimiter) ActiveStreams() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.global
}

// ActiveStreamsForUser returns the active stream count for a user (for testing/metrics).
func (l *StreamLimiter) ActiveStreamsForUser(username string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.perUser[username]
}
