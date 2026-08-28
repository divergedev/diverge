package topology

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddEdge(t *testing.T) {
	g := NewServiceGraph()
	g.AddEdge(Edge{From: "A", To: "B"})
	g.AddEdge(Edge{From: "A", To: "C"})

	edges := g.Edges()
	assert.Len(t, edges, 2)

	// Adding tombstone should be ignored
	g.AddEdge(Edge{From: "B", To: "C", Tombstone: true})
	assert.Len(t, g.Edges(), 2)
}

func TestNeighbors(t *testing.T) {
	g := NewServiceGraph()
	g.AddEdge(Edge{From: "A", To: "B"})
	g.AddEdge(Edge{From: "A", To: "C"})
	g.AddEdge(Edge{From: "A", To: "B"}) // duplicate

	neighbors := g.Neighbors("A")
	expected := []string{"B", "C"}
	assert.Equal(t, expected, neighbors)
}

func TestUpstreamCallers(t *testing.T) {
	g := NewServiceGraph()
	g.AddEdge(Edge{From: "A", To: "C"})
	g.AddEdge(Edge{From: "B", To: "C"})

	callers := g.UpstreamCallers("C")
	expected := []string{"A", "B"}
	assert.Equal(t, expected, callers)
}

func TestValidate_NoCycle(t *testing.T) {
	g := NewServiceGraph()
	g.AddEdge(Edge{From: "A", To: "B"})
	g.AddEdge(Edge{From: "B", To: "C"})

	require.NoError(t, g.Validate())
}

func TestValidate_Cycle(t *testing.T) {
	g := NewServiceGraph()
	g.AddEdge(Edge{From: "A", To: "B"})
	g.AddEdge(Edge{From: "B", To: "C"})
	g.AddEdge(Edge{From: "C", To: "A"})

	err := g.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle detected")

	validCycleMsg := strings.Contains(err.Error(), "A -> B -> C -> A") ||
		strings.Contains(err.Error(), "B -> C -> A -> B") ||
		strings.Contains(err.Error(), "C -> A -> B -> C")
	assert.True(t, validCycleMsg, "unexpected cycle path: %v", err)
}

func TestValidate_SelfLoop(t *testing.T) {
	g := NewServiceGraph()
	g.AddEdge(Edge{From: "A", To: "B"})
	g.AddEdge(Edge{From: "B", To: "B"}) // self-loop

	require.NoError(t, g.Validate())
}

func TestValidate_DiamondNoCycle(t *testing.T) {
	g := NewServiceGraph()
	g.AddEdge(Edge{From: "A", To: "B"})
	g.AddEdge(Edge{From: "A", To: "C"})
	g.AddEdge(Edge{From: "B", To: "D"})
	g.AddEdge(Edge{From: "C", To: "D"})

	require.NoError(t, g.Validate())
}

func TestAllIngressPaths_Linear(t *testing.T) {
	g := NewServiceGraph()
	g.AddEntrypoint("gateway")
	g.AddEdge(Edge{From: "gateway", To: "api"})
	g.AddEdge(Edge{From: "api", To: "payments"})

	paths := g.AllIngressPaths("payments")
	require.Len(t, paths, 1)
	assert.Equal(t, []string{"gateway", "api", "payments"}, paths[0].Hops)
}

func TestAllIngressPaths_MultiGateway(t *testing.T) {
	g := NewServiceGraph()
	g.AddEntrypoint("gateway-ext")
	g.AddEntrypoint("gateway-int")
	g.AddEdge(Edge{From: "gateway-ext", To: "payments"})
	g.AddEdge(Edge{From: "gateway-int", To: "payments"})

	paths := g.AllIngressPaths("payments")
	assert.Len(t, paths, 2)
}

func TestAllIngressPaths_Diamond(t *testing.T) {
	g := NewServiceGraph()
	g.AddEntrypoint("gw")
	g.AddEdge(Edge{From: "gw", To: "router-a"})
	g.AddEdge(Edge{From: "gw", To: "router-b"})
	g.AddEdge(Edge{From: "router-a", To: "svc"})
	g.AddEdge(Edge{From: "router-b", To: "svc"})

	paths := g.AllIngressPaths("svc")
	assert.Len(t, paths, 2)
}

func TestAllIngressPaths_Unreachable(t *testing.T) {
	g := NewServiceGraph()
	g.AddEntrypoint("gateway")
	g.AddEdge(Edge{From: "gateway", To: "api"})
	g.AddEdge(Edge{From: "other", To: "isolated"})

	paths := g.AllIngressPaths("isolated")
	assert.Len(t, paths, 0)
}

func TestAllIngressPaths_MultiHop(t *testing.T) {
	g := NewServiceGraph()
	g.AddEntrypoint("gw")
	g.AddEdge(Edge{From: "gw", To: "a"})
	g.AddEdge(Edge{From: "a", To: "b"})
	g.AddEdge(Edge{From: "b", To: "c"})
	g.AddEdge(Edge{From: "c", To: "target"})

	paths := g.AllIngressPaths("target")
	require.Len(t, paths, 1)
	expected := []string{"gw", "a", "b", "c", "target"}
	assert.Equal(t, expected, paths[0].Hops)
}

func TestMerge_EdgeUnion(t *testing.T) {
	g1 := NewServiceGraph()
	g1.AddEdge(Edge{From: "A", To: "B"})

	g2 := NewServiceGraph()
	g2.AddEdge(Edge{From: "B", To: "C"})

	g3 := g1.Merge(g2)
	assert.Len(t, g3.Edges(), 2)
}

func TestMerge_Tombstone(t *testing.T) {
	g1 := NewServiceGraph()
	g1.AddEdge(Edge{From: "A", To: "B"})

	g2 := NewServiceGraph()
	g2.edges["A"] = append(g2.edges["A"], Edge{From: "A", To: "B", Tombstone: true})

	g3 := g1.Merge(g2)
	assert.Len(t, g3.Edges(), 0)
}

func TestMerge_EntrypointUnion(t *testing.T) {
	g1 := NewServiceGraph()
	g1.AddEntrypoint("gw1")

	g2 := NewServiceGraph()
	g2.AddEntrypoint("gw2")

	g3 := g1.Merge(g2)
	eps := g3.Entrypoints()
	assert.Len(t, eps, 2)
}

func TestMerge_Immutable(t *testing.T) {
	g1 := NewServiceGraph()
	g1.AddEdge(Edge{From: "A", To: "B"})

	g2 := NewServiceGraph()
	g2.AddEdge(Edge{From: "B", To: "C"})

	g3 := g1.Merge(g2)
	assert.Len(t, g1.Edges(), 1)
	assert.Len(t, g2.Edges(), 1)
	assert.Len(t, g3.Edges(), 2)
}

func TestServices(t *testing.T) {
	g := NewServiceGraph()
	g.AddEdge(Edge{From: "C", To: "A"})
	g.AddEntrypoint("B")

	svcs := g.Services()
	expected := []string{"A", "B", "C"}
	assert.Equal(t, expected, svcs)
}

func TestSubgraph(t *testing.T) {
	g := NewServiceGraph()
	g.AddEdge(Edge{From: "A", To: "B"})
	g.AddEdge(Edge{From: "B", To: "C"})
	g.AddEdge(Edge{From: "C", To: "D"})
	g.AddEntrypoint("A")
	g.AddEntrypoint("D")

	sub := g.Subgraph([]string{"B", "C", "D"})
	edges := sub.Edges()
	assert.Len(t, edges, 2)

	eps := sub.Entrypoints()
	require.Len(t, eps, 1)
	assert.Equal(t, "D", eps[0])
}

func TestMerge_PreservesIsolatedServices(t *testing.T) {
	g1 := NewServiceGraph()
	g1.AddEdge(Edge{From: "a", To: "b"})

	g2 := NewServiceGraph()
	g2.AddNode("c")

	g3 := g1.Merge(g2)
	svcs := g3.Services()

	assert.Contains(t, svcs, "a")
	assert.Contains(t, svcs, "b")
	assert.Contains(t, svcs, "c")
	assert.Len(t, svcs, 3)
}

func TestSubgraph_IsolatedNode(t *testing.T) {
	g := NewServiceGraph()
	g.AddEdge(Edge{From: "A", To: "B"})
	g.AddNode("isolated")

	sub := g.Subgraph([]string{"A", "isolated"})
	svcs := sub.Services()
	assert.Contains(t, svcs, "A")
	assert.Contains(t, svcs, "isolated")
	assert.Len(t, svcs, 2)
	assert.Empty(t, sub.Edges())
}

func TestAllIngressPaths_TargetIsEntrypoint(t *testing.T) {
	g := NewServiceGraph()
	g.AddEntrypoint("gateway")
	g.AddEdge(Edge{From: "gateway", To: "api"})
	g.AddEdge(Edge{From: "api", To: "payments"})

	paths := g.AllIngressPaths("gateway")
	require.Len(t, paths, 1)
	assert.Equal(t, []string{"gateway"}, paths[0].Hops)
}

func TestMerge_DedupEdges(t *testing.T) {
	g1 := NewServiceGraph()
	g1.AddEdge(Edge{From: "A", To: "B", Source: "gateway-api"})

	g2 := NewServiceGraph()
	g2.AddEdge(Edge{From: "A", To: "B", Source: "static"})

	g3 := g1.Merge(g2)
	assert.Len(t, g3.Edges(), 1)
}
