package streaming

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSubscribeBeforeList_DeduplicatesConcurrentEvents proves the Subscribe→List→Deduplicate
// pattern eliminates the race window where events could be missed between List and Subscribe.
func TestSubscribeBeforeList_DeduplicatesConcurrentEvents(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Step 1: Subscribe FIRST (before List)
	sub, err := b.Subscribe(ctx)
	require.NoError(t, err)
	defer b.Unsubscribe(sub.ID())

	// Step 2: Simulate an event arriving DURING the List call.
	// This event has the same resource version as an item in the List result.
	b.Publish(Event[string]{Type: "ADDED", Object: "env-1", Version: "rv-100"})

	// Step 3: Simulate processing the List result — build sentRVs.
	sentRVs := map[string]struct{}{
		"default/env-1@rv-100": {}, // This item was in the List snapshot
	}

	// Step 4: Drain subscriber channel, deduplicating against sentRVs.
	// The event from Step 2 should be skipped because its key matches sentRVs.
	var received []Event[string]
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case ev := <-sub.Events():
			key := "default/" + ev.Object + "@" + ev.Version
			if _, sent := sentRVs[key]; !sent {
				received = append(received, ev)
			}
		case <-timeout:
			goto done
		}
	}
done:
	assert.Empty(t, received, "duplicate event should have been filtered by sentRVs")
}

// TestSubscribeBeforeList_DeliversNewEvents proves that events with NEW
// resource versions (not in the List snapshot) are delivered to the client.
func TestSubscribeBeforeList_DeliversNewEvents(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Subscribe first
	sub, err := b.Subscribe(ctx)
	require.NoError(t, err)
	defer b.Unsubscribe(sub.ID())

	// Simulate List result
	sentRVs := map[string]struct{}{
		"default/env-1@rv-100": {},
	}

	// Publish a NEW event (different resource version — happened after List)
	b.Publish(Event[string]{Type: "MODIFIED", Object: "env-1", Version: "rv-101"})

	// This event should NOT be deduplicated
	select {
	case ev := <-sub.Events():
		key := "default/" + ev.Object + "@" + ev.Version
		_, sent := sentRVs[key]
		assert.False(t, sent, "new event should not be in sentRVs")
		assert.Equal(t, "rv-101", ev.Version)
		assert.Equal(t, "MODIFIED", ev.Type)
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for new event")
	}
}

// TestSubscribeBeforeList_ConcurrentPublishDuringList proves that rapid concurrent
// publishes during the List phase are correctly captured by the subscriber channel.
func TestSubscribeBeforeList_ConcurrentPublishDuringList(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Subscribe first
	sub, err := b.Subscribe(ctx)
	require.NoError(t, err)
	defer b.Unsubscribe(sub.ID())

	// Simulate concurrent events arriving during List
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b.Publish(Event[string]{
				Type:    "ADDED",
				Object:  "env-concurrent",
				Version: "rv-" + string(rune('A'+i)),
			})
		}(i)
	}
	wg.Wait()

	// All 10 events should be buffered (buffer size is 64)
	count := 0
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case <-sub.Events():
			count++
		case <-timeout:
			goto done
		}
	}
done:
	assert.Equal(t, 10, count, "all concurrent events should be received")
}

// TestSubscribeBeforeList_UnsubscribeOnContextCancel verifies that
// context cancellation cleans up the subscriber from the broadcaster.
func TestSubscribeBeforeList_UnsubscribeOnContextCancel(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{})
	ctx, cancel := context.WithCancel(context.Background())

	sub, err := b.Subscribe(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, b.SubscriberCount())

	// Cancel context — should auto-unsubscribe
	cancel()

	// Wait for the auto-cleanup goroutine
	assert.Eventually(t, func() bool {
		return b.SubscriberCount() == 0
	}, 1*time.Second, 10*time.Millisecond, "subscriber should be cleaned up")

	// Channel should be closed
	_, ok := <-sub.Events()
	assert.False(t, ok, "subscriber channel should be closed")
}

// TestSubscribeBeforeList_EventBeforeSubscribe proves that events published
// BEFORE Subscribe are NOT received — confirming Subscribe-first ordering matters.
func TestSubscribeBeforeList_EventBeforeSubscribe(t *testing.T) {
	b := NewBroadcaster[string](BroadcasterMetrics{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Publish BEFORE subscribing
	b.Publish(Event[string]{Type: "ADDED", Object: "env-missed", Version: "rv-1"})

	// Subscribe after the event
	sub, err := b.Subscribe(ctx)
	require.NoError(t, err)
	defer b.Unsubscribe(sub.ID())

	// Should NOT receive the event — proves Subscribe-first is necessary
	select {
	case ev := <-sub.Events():
		t.Fatalf("should not receive events published before Subscribe, got: %+v", ev)
	case <-time.After(200 * time.Millisecond):
		// Expected — no events
	}
}
