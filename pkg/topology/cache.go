package topology

import (
	"context"
	"log"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

type topologyCache struct {
	mu           sync.RWMutex
	graph        *ServiceGraph
	lastUpdated  time.Time
	ttl          time.Duration
	pollInterval time.Duration
	sf           singleflight.Group
	discoverer   GraphDiscoverer
	namespaces   []string
	cancel       context.CancelFunc
}

func NewTopologyCache(d GraphDiscoverer, namespaces []string, pollInterval, ttl time.Duration) *topologyCache {
	return &topologyCache{
		graph:        NewServiceGraph(),
		ttl:          ttl,
		pollInterval: pollInterval,
		discoverer:   d,
		namespaces:   namespaces,
	}
}

func (c *topologyCache) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	go func() {
		ticker := time.NewTicker(c.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = c.refresh(ctx)
			}
		}
	}()
}

func (c *topologyCache) refresh(ctx context.Context) (*ServiceGraph, error) {
	v, err, _ := c.sf.Do("refresh", func() (interface{}, error) {
		g, err := c.discoverer.Discover(ctx, c.namespaces)
		if err != nil {
			log.Printf("Warning: failed to discover topology: %v", err)
			return nil, err
		}

		c.mu.Lock()
		c.graph = g
		c.lastUpdated = time.Now()
		c.mu.Unlock()

		return g, nil
	})

	if err != nil {
		return nil, err
	}
	return v.(*ServiceGraph), nil
}

func (c *topologyCache) Get(ctx context.Context) *ServiceGraph {
	c.mu.RLock()
	lastUpdated := c.lastUpdated
	graph := c.graph
	c.mu.RUnlock()

	if time.Since(lastUpdated) > c.ttl {
		// stale-while-revalidate: trigger async refresh but return current data
		go func() { _, _ = c.refresh(context.Background()) }()
	}

	return graph
}

func (c *topologyCache) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}
