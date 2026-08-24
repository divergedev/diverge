package server

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/divergedev/diverge/internal/server/auth"
)

func ctxWithUser(username string) context.Context {
	return auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{
		Username: username,
	})
}

func TestStreamLimiter_BasicAcquireRelease(t *testing.T) {
	l := NewStreamLimiter(10, 5)

	release, err := l.Acquire(ctxWithUser("alice"))
	require.NoError(t, err)
	assert.Equal(t, 1, l.ActiveStreams())
	assert.Equal(t, 1, l.ActiveStreamsForUser("alice"))

	release()
	assert.Equal(t, 0, l.ActiveStreams())
	assert.Equal(t, 0, l.ActiveStreamsForUser("alice"))
}

func TestStreamLimiter_PerUserLimit(t *testing.T) {
	l := NewStreamLimiter(100, 3)

	var releases []func()
	for i := 0; i < 3; i++ {
		release, err := l.Acquire(ctxWithUser("alice"))
		require.NoError(t, err)
		releases = append(releases, release)
	}

	// Alice at limit
	_, err := l.Acquire(ctxWithUser("alice"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stream limit of 3 reached")
	assert.Contains(t, err.Error(), "alice")

	// Bob unaffected
	release, err := l.Acquire(ctxWithUser("bob"))
	require.NoError(t, err)
	release()

	// Release one of Alice's — she can acquire again
	releases[0]()
	release, err = l.Acquire(ctxWithUser("alice"))
	require.NoError(t, err)
	release()

	for _, r := range releases[1:] {
		r()
	}
}

func TestStreamLimiter_GlobalLimit(t *testing.T) {
	l := NewStreamLimiter(3, 2)

	r1, err := l.Acquire(ctxWithUser("alice"))
	require.NoError(t, err)
	r2, err := l.Acquire(ctxWithUser("alice"))
	require.NoError(t, err)
	r3, err := l.Acquire(ctxWithUser("bob"))
	require.NoError(t, err)

	// Global limit reached (3/3)
	_, err = l.Acquire(ctxWithUser("charlie"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "server stream capacity reached")

	r1()
	r2()
	r3()
}

func TestStreamLimiter_FailClosedOnMissingUser(t *testing.T) {
	l := NewStreamLimiter(10, 5)

	// No user in context
	release, err := l.Acquire(context.Background())
	assert.Error(t, err)
	assert.NotNil(t, release, "release must never be nil")
	connectErr := new(connect.Error)
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeUnauthenticated, connectErr.Code())

	// Empty username
	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: ""})
	release, err = l.Acquire(ctx)
	assert.Error(t, err)
	assert.NotNil(t, release, "release must never be nil")
}

func TestStreamLimiter_DoubleRelease(t *testing.T) {
	l := NewStreamLimiter(10, 5)
	release, err := l.Acquire(ctxWithUser("alice"))
	require.NoError(t, err)

	release()
	release() // Should not panic or double-decrement
	assert.Equal(t, 0, l.ActiveStreams())
}

func TestStreamLimiter_ZeroCountCleanup(t *testing.T) {
	l := NewStreamLimiter(10, 5)
	release, err := l.Acquire(ctxWithUser("alice"))
	require.NoError(t, err)
	release()

	l.mu.Lock()
	_, exists := l.perUser["alice"]
	l.mu.Unlock()
	assert.False(t, exists, "zero-count user entry should be deleted")
}

func TestStreamLimiter_ConcurrentAcquireRelease(t *testing.T) {
	l := NewStreamLimiter(100, 50)
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := ctxWithUser("alice")
			release, err := l.Acquire(ctx)
			if err != nil {
				return // over limit, expected
			}
			// Simulate work
			release()
		}(i)
	}

	wg.Wait()
	assert.Equal(t, 0, l.ActiveStreams())
}

func TestStreamLimiter_ErrorCodesAreCorrect(t *testing.T) {
	l := NewStreamLimiter(2, 1)

	r1, err := l.Acquire(ctxWithUser("alice"))
	require.NoError(t, err)

	// Per-user limit → ResourceExhausted
	_, err = l.Acquire(ctxWithUser("alice"))
	connectErr := new(connect.Error)
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeResourceExhausted, connectErr.Code())

	r2, err := l.Acquire(ctxWithUser("bob"))
	require.NoError(t, err)

	// Global limit → ResourceExhausted
	_, err = l.Acquire(ctxWithUser("charlie"))
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeResourceExhausted, connectErr.Code())

	r1()
	r2()
}

func TestStreamLimiter_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		maxGlobal := rapid.IntRange(1, 50).Draw(t, "maxGlobal")
		maxPerUser := rapid.IntRange(1, maxGlobal).Draw(t, "maxPerUser")
		l := NewStreamLimiter(maxGlobal, maxPerUser)

		nUsers := rapid.IntRange(1, 5).Draw(t, "users")
		nOps := rapid.IntRange(0, 100).Draw(t, "ops")

		// Shadow model: track expected counts
		shadow := make(map[string]int)
		shadowGlobal := 0
		releases := make(map[string][]func())

		for i := 0; i < nOps; i++ {
			userIdx := rapid.IntRange(0, nUsers-1).Draw(t, "userIdx")
			username := fmt.Sprintf("user-%d", userIdx)

			if rapid.Bool().Draw(t, "acquire") {
				ctx := ctxWithUser(username)
				release, err := l.Acquire(ctx)

				// Verify error correctness against shadow model
				if shadow[username] >= maxPerUser {
					if err == nil {
						t.Fatalf("step %d: expected per-user limit error for %s (shadow=%d, max=%d)", i, username, shadow[username], maxPerUser)
					}
				} else if shadowGlobal >= maxGlobal {
					if err == nil {
						t.Fatalf("step %d: expected global limit error (shadow=%d, max=%d)", i, shadowGlobal, maxGlobal)
					}
				}

				if err == nil {
					shadow[username]++
					shadowGlobal++
					releases[username] = append(releases[username], release)
				}
			} else {
				if rs := releases[username]; len(rs) > 0 {
					rs[0]()
					releases[username] = rs[1:]
					shadow[username]--
					shadowGlobal--
				}
			}

			// Step invariants
			if l.ActiveStreams() != shadowGlobal {
				t.Fatalf("step %d: ActiveStreams=%d, shadow=%d", i, l.ActiveStreams(), shadowGlobal)
			}
			for u, count := range shadow {
				if l.ActiveStreamsForUser(u) != count {
					t.Fatalf("step %d: ActiveStreamsForUser(%s)=%d, shadow=%d", i, u, l.ActiveStreamsForUser(u), count)
				}
			}
			if shadowGlobal < 0 || shadowGlobal > maxGlobal {
				t.Fatalf("step %d: shadowGlobal %d out of range [0, %d]", i, shadowGlobal, maxGlobal)
			}
		}

		// Release all
		for _, rs := range releases {
			for _, r := range rs {
				r()
			}
		}

		if l.ActiveStreams() != 0 {
			t.Fatalf("active streams should be 0, got %d", l.ActiveStreams())
		}
	})
}

func TestNewStreamLimiter_Panics(t *testing.T) {
	tests := []struct {
		name       string
		maxGlobal  int
		maxPerUser int
	}{
		{"maxGlobal zero", 0, 1},
		{"maxGlobal negative", -1, 1},
		{"maxPerUser zero", 10, 0},
		{"maxPerUser negative", 10, -1},
		{"maxPerUser exceeds maxGlobal", 10, 11},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Panics(t, func() {
				NewStreamLimiter(tt.maxGlobal, tt.maxPerUser)
			})
		})
	}
}

func BenchmarkStreamLimiter_AcquireRelease(b *testing.B) {
	l := NewStreamLimiter(100000, 100000)
	ctx := ctxWithUser("bench-user")
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			release, err := l.Acquire(ctx)
			if err != nil {
				b.Fatal(err)
			}
			release()
		}
	})
}
