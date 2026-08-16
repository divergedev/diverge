package streaming

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

const defaultBufferSize = 64

// Event represents a watch event.
type Event[T any] struct {
	Type    string // ADDED, MODIFIED, DELETED
	Object  T
	Version string // resourceVersion
}

// Subscriber is a registered watch subscriber.
type Subscriber[T any] struct {
	ch     chan Event[T]
	ctx    context.Context
	cancel context.CancelFunc
	id     string
}

// Broadcaster distributes events to multiple subscribers with bounded buffers.
type Broadcaster[T any] struct {
	mu          sync.RWMutex
	subscribers map[string]*Subscriber[T]
}

// NewBroadcaster creates a new bounded broadcaster.
func NewBroadcaster[T any]() *Broadcaster[T] {
	return &Broadcaster[T]{
		subscribers: make(map[string]*Subscriber[T]),
	}
}

// Subscribe creates a new subscriber with a bounded channel.
// When the buffer is full, the subscriber is dropped (closed).
func (b *Broadcaster[T]) Subscribe(ctx context.Context) *Subscriber[T] {
	ctx, cancel := context.WithCancel(ctx)
	sub := &Subscriber[T]{
		ch:     make(chan Event[T], defaultBufferSize),
		ctx:    ctx,
		cancel: cancel,
		id:     generateID(),
	}
	b.mu.Lock()
	b.subscribers[sub.id] = sub
	b.mu.Unlock()

	// Auto-unsubscribe on context cancellation
	go func() {
		<-ctx.Done()
		b.Unsubscribe(sub.id)
	}()

	return sub
}

// Unsubscribe removes a subscriber.
func (b *Broadcaster[T]) Unsubscribe(id string) {
	b.mu.Lock()
	if sub, ok := b.subscribers[id]; ok {
		sub.cancel()
		close(sub.ch)
		delete(b.subscribers, id)
	}
	b.mu.Unlock()
}

// Publish sends an event to all subscribers.
// If a subscriber's buffer is full, it is dropped.
func (b *Broadcaster[T]) Publish(event Event[T]) {
	b.mu.RLock()
	// Create a copy of the keys to avoid holding the lock during drop
	var toDrop []string
	for id, sub := range b.subscribers {
		select {
		case sub.ch <- event:
			// sent successfully
		default:
			// buffer full, drop subscriber
			toDrop = append(toDrop, id)
		}
	}
	b.mu.RUnlock()

	for _, id := range toDrop {
		b.Unsubscribe(id)
	}
}

// Events returns the subscriber's event channel.
func (s *Subscriber[T]) Events() <-chan Event[T] {
	return s.ch
}

// ID returns the subscriber's ID.
func (s *Subscriber[T]) ID() string {
	return s.id
}

// SubscriberCount returns number of active subscribers.
func (b *Broadcaster[T]) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

func generateID() string {
	return uuid.New().String()
}
