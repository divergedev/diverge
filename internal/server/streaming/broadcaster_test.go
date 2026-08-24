package streaming

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestBroadcaster_PublishReceive(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := b.Subscribe(ctx)
	require.NoError(t, err)

	go func() {
		b.Publish(Event[string]{Type: "ADDED", Object: "foo", Version: "1"})
	}()

	select {
	case ev := <-sub.Events():
		assert.Equal(t, "foo", ev.Object)
	case <-time.After(1 * time.Second):
		t.Fatal("timeout")
	}
}

func TestBroadcaster_SlowConsumerDropped(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{}, WithBufferSize(4))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := b.Subscribe(ctx)
	require.NoError(t, err)

	// Fill buffer
	for i := 0; i < 4; i++ {
		b.Publish(Event[string]{Type: "ADDED", Object: "foo", Version: "1"})
	}

	// This should drop the consumer
	b.Publish(Event[string]{Type: "ADDED", Object: "foo", Version: "1"})

	assert.Eventually(t, func() bool {
		return b.SubscriberCount() == 0
	}, 1*time.Second, 10*time.Millisecond, "expected subscriber to be dropped")

	// Drain the buffered channel
	for i := 0; i < 4; i++ {
		<-sub.Events()
	}

	// The channel should be closed
	_, ok := <-sub.Events()
	assert.False(t, ok, "expected channel to be closed")
}

func TestBroadcaster_ContextCancellation(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{})
	ctx, cancel := context.WithCancel(context.Background())
	_, err := b.Subscribe(ctx)
	require.NoError(t, err)

	assert.Equal(t, 1, b.SubscriberCount())

	cancel()

	assert.Eventually(t, func() bool {
		return b.SubscriberCount() == 0
	}, 1*time.Second, 10*time.Millisecond, "expected subscriber to be dropped on cancel")
}

func TestBroadcaster_ConcurrentPublish(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 5; i++ {
		_, err := b.Subscribe(ctx)
		require.NoError(t, err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b.Publish(Event[string]{Type: "ADDED", Object: "foo", Version: "1"})
		}(i)
	}

	wg.Wait()

	assert.Equal(t, 5, b.SubscriberCount())
}

func TestBroadcaster_Unsubscribe(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := b.Subscribe(ctx)
	require.NoError(t, err)

	assert.Equal(t, 1, b.SubscriberCount())
	b.Unsubscribe(sub.id)
	assert.Equal(t, 0, b.SubscriberCount())
}

func TestBroadcaster_Close(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 5; i++ {
		_, err := b.Subscribe(ctx)
		require.NoError(t, err)
	}
	assert.Equal(t, 5, b.SubscriberCount())

	b.Close()
	assert.Equal(t, 0, b.SubscriberCount())

	// Subscribe after Close should return error
	_, err := b.Subscribe(ctx)
	assert.ErrorIs(t, err, ErrBroadcasterClosed)

	// Publish after Close is a no-op (no panic)
	b.Publish(Event[string]{Type: "ADDED", Object: "foo"})
}

func TestBroadcaster_Close_Idempotent(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{})
	b.Close()
	b.Close() // Should not panic
}

func TestBroadcaster_Close_ConcurrentWithPublish(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{}, WithBufferSize(128))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 20; i++ {
		_, err := b.Subscribe(ctx)
		require.NoError(t, err)
	}

	// Race: concurrent Close, Publish, and Subscribe
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		b.Close()
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			b.Publish(Event[string]{Type: "ADDED", Object: "foo"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_, _ = b.Subscribe(ctx) // may fail with ErrBroadcasterClosed
		}
	}()
	wg.Wait()

	assert.Equal(t, 0, b.SubscriberCount())
}

func TestBroadcaster_Close_ConcurrentSubscribeRace(t *testing.T) {
	// Stress test: many goroutines racing Subscribe vs Close
	b := NewBroadcaster[string](BroadcasterMetrics{}, WithBufferSize(4))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = b.Subscribe(ctx)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.Close()
	}()
	wg.Wait()
}

func TestBroadcaster_Close_DecSubscribersCalled(t *testing.T) {
	var decCount int
	var mu sync.Mutex
	metrics := BroadcasterMetrics{
		IncSubscribers: func() {},
		DecSubscribers: func() {
			mu.Lock()
			decCount++
			mu.Unlock()
		},
	}

	b := NewBroadcaster[string](metrics)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 3; i++ {
		_, err := b.Subscribe(ctx)
		require.NoError(t, err)
	}

	b.Close()

	mu.Lock()
	assert.Equal(t, 3, decCount, "DecSubscribers should be called for each subscriber on Close")
	mu.Unlock()
}

func TestBroadcaster_SlowConsumerDoesNotAffectFast(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{}, WithBufferSize(2))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slow, err := b.Subscribe(ctx)
	require.NoError(t, err)
	fast, err := b.Subscribe(ctx)
	require.NoError(t, err)

	// Fill slow's buffer
	b.Publish(Event[string]{Type: "ADDED", Object: "a"})
	b.Publish(Event[string]{Type: "ADDED", Object: "b"})

	// Drain fast
	<-fast.Events()
	<-fast.Events()

	// Overflow slow — should drop slow but keep fast
	b.Publish(Event[string]{Type: "ADDED", Object: "c"})

	assert.Eventually(t, func() bool {
		return b.SubscriberCount() == 1
	}, 1*time.Second, 10*time.Millisecond, "slow sub should be dropped")

	// Drain slow's buffered events
	<-slow.Events()
	<-slow.Events()
	_, ok := <-slow.Events()
	assert.False(t, ok, "slow channel should be closed")

	// Fast should still receive — it got 'c' too
	ev := <-fast.Events()
	assert.Equal(t, "c", ev.Object)

	// And new events after slow was dropped
	b.Publish(Event[string]{Type: "ADDED", Object: "d"})
	ev = <-fast.Events()
	assert.Equal(t, "d", ev.Object)
}

func TestBroadcaster_WithBufferSize(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{}, WithBufferSize(2))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := b.Subscribe(ctx)
	require.NoError(t, err)

	// Buffer size 2: fill it
	b.Publish(Event[string]{Type: "ADDED", Object: "a"})
	b.Publish(Event[string]{Type: "ADDED", Object: "b"})

	// Third should drop the subscriber
	b.Publish(Event[string]{Type: "ADDED", Object: "c"})

	assert.Eventually(t, func() bool {
		return b.SubscriberCount() == 0
	}, 1*time.Second, 10*time.Millisecond)

	// Drain and verify closed
	<-sub.Events()
	<-sub.Events()
	_, ok := <-sub.Events()
	assert.False(t, ok)
}

func TestBroadcaster_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		bufSize := rapid.IntRange(1, 32).Draw(t, "bufferSize")
		b := NewBroadcaster[string](BroadcasterMetrics{}, WithBufferSize(bufSize))
		nSubs := rapid.IntRange(1, 10).Draw(t, "subscribers")
		nEvents := rapid.IntRange(0, 50).Draw(t, "events")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		subs := make([]*Subscriber[string], 0, nSubs)
		for i := 0; i < nSubs; i++ {
			sub, err := b.Subscribe(ctx)
			if err != nil {
				t.Fatalf("subscribe failed: %v", err)
			}
			subs = append(subs, sub)
		}

		// Invariant: subscriber count matches
		if b.SubscriberCount() != len(subs) {
			t.Fatalf("expected %d subscribers, got %d", len(subs), b.SubscriberCount())
		}

		// Publish events — some subscribers may be dropped
		for i := 0; i < nEvents; i++ {
			b.Publish(Event[string]{Type: "MODIFIED", Object: fmt.Sprintf("event-%d", i)})
		}

		// Invariant: subscriber count is non-negative and <= initial
		count := b.SubscriberCount()
		if count < 0 || count > nSubs {
			t.Fatalf("subscriber count %d out of range [0, %d]", count, nSubs)
		}

		// If events <= bufSize, no subscribers should have been dropped
		if nEvents <= bufSize && count != nSubs {
			t.Fatalf("with %d events and buffer %d, expected %d subs, got %d",
				nEvents, bufSize, nSubs, count)
		}

		// Close should bring count to 0
		b.Close()
		if b.SubscriberCount() != 0 {
			t.Fatalf("expected 0 after Close, got %d", b.SubscriberCount())
		}

		// Double close is safe
		b.Close()
	})
}

func BenchmarkBroadcaster_Publish(b *testing.B) {
	br := NewBroadcaster[string](BroadcasterMetrics{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < 10; i++ {
		_, _ = br.Subscribe(ctx)
	}
	event := Event[string]{Type: "MODIFIED", Object: "test"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		br.Publish(event)
	}
}
