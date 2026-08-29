package topology

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"time"

	"github.com/stretchr/testify/assert"
)

type mockCacheDiscoverer struct {
	graph *ServiceGraph
	err   error
	calls int32
}

func (m *mockCacheDiscoverer) Discover(ctx context.Context, namespaces []string) (*ServiceGraph, error) {
	atomic.AddInt32(&m.calls, 1)
	return m.graph, m.err
}

func (m *mockCacheDiscoverer) Name() string {
	return "mock"
}

func TestCache_HitWithinTTL(t *testing.T) {
	d := &mockCacheDiscoverer{graph: NewServiceGraph()}
	c := NewTopologyCache(d, nil, 10*time.Minute, 5*time.Minute)
	c.lastUpdated = time.Now()

	g := c.Get(context.Background())
	assert.NotNil(t, g)
	assert.Equal(t, int32(0), atomic.LoadInt32(&d.calls))
}

func TestCache_StaleWhileRevalidate(t *testing.T) {
	g := NewServiceGraph()
	g.AddNode("test")
	d := &mockCacheDiscoverer{graph: g}
	c := NewTopologyCache(d, nil, 10*time.Minute, 5*time.Millisecond)

	c.lastUpdated = time.Now().Add(-10 * time.Millisecond)

	ret := c.Get(context.Background())
	assert.NotNil(t, ret)

	// wait for async refresh
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), atomic.LoadInt32(&d.calls))
	refreshed := c.Get(context.Background())
	assert.Len(t, refreshed.Services(), 1)
}

func TestCache_Stop(t *testing.T) {
	d := &mockCacheDiscoverer{graph: NewServiceGraph()}
	c := NewTopologyCache(d, nil, 10*time.Millisecond, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)

	time.Sleep(50 * time.Millisecond)
	calls1 := atomic.LoadInt32(&d.calls)
	assert.Greater(t, calls1, int32(0))

	c.Stop()
	cancel()

	time.Sleep(50 * time.Millisecond)
	calls2 := atomic.LoadInt32(&d.calls)

	time.Sleep(50 * time.Millisecond)
	calls3 := atomic.LoadInt32(&d.calls)
	assert.Equal(t, calls2, calls3)
}

func TestCache_ErrorFallback(t *testing.T) {
	g := NewServiceGraph()
	g.AddNode("test")
	d := &mockCacheDiscoverer{graph: g, err: errors.New("fail")}
	c := NewTopologyCache(d, nil, 10*time.Minute, 5*time.Minute)

	c.graph = g
	c.lastUpdated = time.Now().Add(-10 * time.Minute)

	ret := c.Get(context.Background())
	assert.Equal(t, g, ret)

	time.Sleep(50 * time.Millisecond)

	c.mu.RLock()
	assert.Equal(t, g, c.graph) // still old graph
	c.mu.RUnlock()
}

func TestCache_Singleflight(t *testing.T) {
	d := &mockCacheDiscoverer{graph: NewServiceGraph()}
	c := NewTopologyCache(d, nil, 10*time.Minute, 5*time.Minute)

	// Block discover until ready
	ready := make(chan struct{})
	done := make(chan struct{})

	d2 := &mockCacheDiscoverer{graph: NewServiceGraph()}
	c.discoverer = mockCacheDiscovererBlocker{mockCacheDiscoverer: d2, ready: ready, done: done}

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.refresh(context.Background())
		}()
	}

	time.Sleep(50 * time.Millisecond)
	close(ready)

	// Wait for Discover to complete (it should only be called once)
	<-done

	// Wait for all refreshes to return
	wg.Wait()

	assert.Equal(t, int32(1), atomic.LoadInt32(&d2.calls))
}

type mockCacheDiscovererBlocker struct {
	*mockCacheDiscoverer
	ready chan struct{}
	done  chan struct{}
}

func (m mockCacheDiscovererBlocker) Discover(ctx context.Context, namespaces []string) (*ServiceGraph, error) {
	<-m.ready
	defer func() { m.done <- struct{}{} }()
	return m.mockCacheDiscoverer.Discover(ctx, namespaces)
}
