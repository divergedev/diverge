package streaming

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"
)

const defaultBufferSize = 1024

// ErrBroadcasterClosed is returned when Subscribe is called on a closed broadcaster.
var ErrBroadcasterClosed = errors.New("broadcaster closed")

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

// BroadcasterMetrics defines optional callbacks for observability.
type BroadcasterMetrics struct {
	IncSubscribers func()
	DecSubscribers func()
	IncEvents      func()
	IncDrops       func()
}

// BroadcasterOption configures a Broadcaster.
type BroadcasterOption func(*broadcasterConfig)

type broadcasterConfig struct {
	bufferSize int
}

// WithBufferSize sets the subscriber channel buffer size.
// Defaults to 1024 if not specified.
func WithBufferSize(n int) BroadcasterOption {
	return func(c *broadcasterConfig) {
		if n > 0 {
			c.bufferSize = n
		}
	}
}

// Broadcaster distributes events to multiple subscribers with bounded buffers.
type Broadcaster[T any] struct {
	mu          sync.RWMutex
	subscribers map[string]*Subscriber[T]
	metrics     BroadcasterMetrics
	bufferSize  int
	closed      bool
}

// NewBroadcaster creates a new bounded broadcaster.
func NewBroadcaster[T any](metrics BroadcasterMetrics, opts ...BroadcasterOption) *Broadcaster[T] {
	cfg := &broadcasterConfig{bufferSize: defaultBufferSize}
	for _, opt := range opts {
		opt(cfg)
	}
	return &Broadcaster[T]{
		subscribers: make(map[string]*Subscriber[T]),
		metrics:     metrics,
		bufferSize:  cfg.bufferSize,
	}
}

// Subscribe creates a new subscriber with a bounded channel.
// When the buffer is full, the subscriber is dropped (closed).
// Returns ErrBroadcasterClosed if the broadcaster has been closed.
func (b *Broadcaster[T]) Subscribe(ctx context.Context) (*Subscriber[T], error) {
	ctx, cancel := context.WithCancel(ctx)
	sub := &Subscriber[T]{
		ch:     make(chan Event[T], b.bufferSize),
		ctx:    ctx,
		cancel: cancel,
		id:     generateID(),
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		cancel()
		return nil, ErrBroadcasterClosed
	}
	b.subscribers[sub.id] = sub
	b.mu.Unlock()

	if b.metrics.IncSubscribers != nil {
		b.metrics.IncSubscribers()
	}

	// Auto-unsubscribe on context cancellation
	go func() {
		<-ctx.Done()
		b.Unsubscribe(sub.id)
	}()

	return sub, nil
}

// Unsubscribe removes a subscriber.
func (b *Broadcaster[T]) Unsubscribe(id string) {
	b.mu.Lock()
	if sub, ok := b.subscribers[id]; ok {
		sub.cancel()
		close(sub.ch)
		delete(b.subscribers, id)
		if b.metrics.DecSubscribers != nil {
			b.metrics.DecSubscribers()
		}
	}
	b.mu.Unlock()
}

// Publish sends an event to all subscribers.
// If a subscriber's buffer is full, it is dropped.
// No-op if the broadcaster is closed.
func (b *Broadcaster[T]) Publish(event Event[T]) {
	if b.metrics.IncEvents != nil {
		b.metrics.IncEvents()
	}
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return
	}
	// Create a copy of the keys to avoid holding the lock during drop
	var toDrop []string
	for id, sub := range b.subscribers {
		select {
		case sub.ch <- event:
			// sent successfully
		default:
			// buffer full, drop subscriber
			toDrop = append(toDrop, id)
			if b.metrics.IncDrops != nil {
				b.metrics.IncDrops()
			}
		}
	}
	b.mu.RUnlock()

	for _, id := range toDrop {
		b.Unsubscribe(id)
	}
}

// Close cleans up all active subscribers and closes their channels.
// Safe to call multiple times. After Close, Subscribe returns ErrBroadcasterClosed.
func (b *Broadcaster[T]) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, sub := range b.subscribers {
		sub.cancel()
		close(sub.ch)
		delete(b.subscribers, id)
		if b.metrics.DecSubscribers != nil {
			b.metrics.DecSubscribers()
		}
	}
}

// IsClosed reports whether the broadcaster has been closed (server shutdown).
// Watch handlers use this to distinguish shutdown from slow-consumer drops.
func (b *Broadcaster[T]) IsClosed() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.closed
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
