package topology

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockDiscoverer struct {
	name  string
	graph *ServiceGraph
	err   error
}

func (m *mockDiscoverer) Discover(ctx context.Context, namespaces []string) (*ServiceGraph, error) {
	return m.graph, m.err
}

func (m *mockDiscoverer) Name() string { return m.name }

func TestCompositeDiscoverer(t *testing.T) {
	t.Run("MergesAll", func(t *testing.T) {
		g1 := NewServiceGraph()
		g1.AddEdge(Edge{From: "a", To: "b", Source: "mock1"})

		g2 := NewServiceGraph()
		g2.AddEdge(Edge{From: "c", To: "d", Source: "mock2"})

		c := &CompositeDiscoverer{
			Discoverers: []GraphDiscoverer{
				&mockDiscoverer{name: "mock1", graph: g1},
				&mockDiscoverer{name: "mock2", graph: g2},
			},
		}

		g, err := c.Discover(context.Background(), nil)
		require.NoError(t, err)

		edges := g.Edges()
		assert.Len(t, edges, 2)
		assert.Equal(t, "mock1+mock2", c.GraphSource())
	})

	t.Run("GracefulDegradation", func(t *testing.T) {
		g2 := NewServiceGraph()
		g2.AddEdge(Edge{From: "c", To: "d", Source: "mock2"})

		c := &CompositeDiscoverer{
			Discoverers: []GraphDiscoverer{
				&mockDiscoverer{name: "mock1", err: errors.New("fail")},
				&mockDiscoverer{name: "mock2", graph: g2},
			},
		}

		g, err := c.Discover(context.Background(), nil)
		require.NoError(t, err)

		edges := g.Edges()
		assert.Len(t, edges, 1)
		assert.Equal(t, "mock2", c.GraphSource())
	})

	t.Run("Empty", func(t *testing.T) {
		c := &CompositeDiscoverer{}
		g, err := c.Discover(context.Background(), nil)
		require.NoError(t, err)
		assert.Empty(t, g.Edges())
		assert.Equal(t, "", c.GraphSource())
	})
}

func TestStaticDiscoverer(t *testing.T) {
	t.Run("DependsOn", func(t *testing.T) {
		d := &StaticDiscoverer{
			Config: StaticConfig{
				Services: map[string]StaticServiceConfig{
					"web": {DependsOn: []string{"api"}},
				},
			},
		}

		g, err := d.Discover(context.Background(), nil)
		require.NoError(t, err)

		edges := g.Edges()
		require.Len(t, edges, 1)
		assert.Equal(t, "web", edges[0].From)
		assert.Equal(t, "api", edges[0].To)
		assert.Equal(t, "static", edges[0].Source)
	})

	t.Run("Entrypoints", func(t *testing.T) {
		d := &StaticDiscoverer{
			Config: StaticConfig{
				Services: map[string]StaticServiceConfig{
					"web": {Entrypoint: true},
				},
			},
		}

		g, err := d.Discover(context.Background(), nil)
		require.NoError(t, err)

		eps := g.Entrypoints()
		require.Len(t, eps, 1)
		assert.Equal(t, "web", eps[0])
	})

	t.Run("Empty", func(t *testing.T) {
		d := &StaticDiscoverer{}
		g, err := d.Discover(context.Background(), nil)
		require.NoError(t, err)
		assert.Empty(t, g.Edges())
		assert.Empty(t, g.Entrypoints())
	})

	t.Run("NoDeps", func(t *testing.T) {
		d := &StaticDiscoverer{
			Config: StaticConfig{
				Services: map[string]StaticServiceConfig{
					"web": {},
				},
			},
		}

		g, err := d.Discover(context.Background(), nil)
		require.NoError(t, err)
		assert.Empty(t, g.Edges())

		svcs := g.Services()
		require.Len(t, svcs, 1)
		assert.Equal(t, "web", svcs[0])
	})
}
