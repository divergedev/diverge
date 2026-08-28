package topology

import (
	"context"
	"strings"
	"sync"
)

// GraphDiscoverer discovers service-to-service edges from a specific source.
type GraphDiscoverer interface {
	// Discover returns a ServiceGraph with edges discovered from this source.
	// namespaces limits discovery to specific Kubernetes namespaces (empty = all).
	Discover(ctx context.Context, namespaces []string) (*ServiceGraph, error)
	// Name returns the human-readable source name (e.g. "gateway-api", "prometheus", "static").
	Name() string
}

// CompositeDiscoverer merges results from multiple discoverers.
// Discoverers are applied in order; later discoverers override earlier ones.
// Each discoverer failure is logged as a warning but does not stop resolution
// (graceful degradation).
type CompositeDiscoverer struct {
	Discoverers []GraphDiscoverer
	Logger      func(format string, args ...any) // optional logger, defaults to no-op

	mu         sync.Mutex
	lastSource string
}

func (c *CompositeDiscoverer) Discover(ctx context.Context, namespaces []string) (*ServiceGraph, error) {
	combined := NewServiceGraph()
	var sources []string

	for _, d := range c.Discoverers {
		graph, err := d.Discover(ctx, namespaces)
		if err != nil {
			if c.Logger != nil {
				c.Logger("discoverer %s failed: %v", d.Name(), err)
			}
			continue
		}

		sources = append(sources, d.Name())
		combined = combined.Merge(graph)
	}

	c.mu.Lock()
	c.lastSource = strings.Join(sources, "+")
	c.mu.Unlock()
	return combined, nil
}

func (c *CompositeDiscoverer) Name() string { return "composite" }

// GraphSource returns a descriptive string of which discoverers contributed
// to the graph (e.g. "gateway-api+prometheus+static").
func (c *CompositeDiscoverer) GraphSource() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastSource
}
