package streaming

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBroadcaster_PublishReceive(t *testing.T) {
	b := NewBroadcaster[string]()
	ctx := context.Background()
	sub := b.Subscribe(ctx)

	go func() {
		b.Publish(Event[string]{Type: "ADDED", Object: "foo", Version: "1"})
	}()

	select {
	case ev := <-sub.Events():
		if ev.Object != "foo" {
			t.Errorf("expected foo, got %v", ev.Object)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout")
	}
}

func TestBroadcaster_SlowConsumerDropped(t *testing.T) {
	b := NewBroadcaster[string]()
	ctx := context.Background()
	sub := b.Subscribe(ctx)

	// Fill buffer
	for i := 0; i < defaultBufferSize; i++ {
		b.Publish(Event[string]{Type: "ADDED", Object: "foo", Version: "1"})
	}

	// This should drop the consumer
	b.Publish(Event[string]{Type: "ADDED", Object: "foo", Version: "1"})

	// Give it a moment to unsubscribe
	time.Sleep(50 * time.Millisecond)

	if b.SubscriberCount() != 0 {
		t.Errorf("expected subscriber to be dropped")
	}

	// Drain the buffered channel
	for i := 0; i < defaultBufferSize; i++ {
		<-sub.Events()
	}

	// The channel should be closed
	_, ok := <-sub.Events()
	if ok {
		t.Error("expected channel to be closed")
	}
}

func TestBroadcaster_ContextCancellation(t *testing.T) {
	b := NewBroadcaster[string]()
	ctx, cancel := context.WithCancel(context.Background())
	_ = b.Subscribe(ctx)

	if b.SubscriberCount() != 1 {
		t.Errorf("expected 1 subscriber")
	}

	cancel()

	time.Sleep(50 * time.Millisecond)

	if b.SubscriberCount() != 0 {
		t.Errorf("expected subscriber to be dropped on cancel")
	}
}

func TestBroadcaster_ConcurrentPublish(t *testing.T) {
	b := NewBroadcaster[string]()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_ = b.Subscribe(ctx)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Need to be careful not to overflow the buffer since we don't drain
			// We publish 10 total events across all goroutines, buffer size is 64
			b.Publish(Event[string]{Type: "ADDED", Object: "foo", Version: "1"})
		}(i)
	}

	wg.Wait()

	if b.SubscriberCount() != 5 {
		t.Errorf("expected 5 subscribers")
	}
}

func TestBroadcaster_Unsubscribe(t *testing.T) {
	b := NewBroadcaster[string]()
	ctx := context.Background()
	sub := b.Subscribe(ctx)

	if b.SubscriberCount() != 1 {
		t.Errorf("expected 1 subscriber")
	}

	b.Unsubscribe(sub.id)

	if b.SubscriberCount() != 0 {
		t.Errorf("expected 0 subscribers")
	}
}
