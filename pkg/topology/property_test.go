package topology

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
)

func TestServiceGraph_ValidDAGAlwaysPasses_PBT(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		numNodes := hegel.Draw(ht, hegel.Integers(2, 12))
		nodes := make([]string, numNodes)
		g := NewServiceGraph()
		for i := 0; i < numNodes; i++ {
			nodes[i] = fmt.Sprintf("svc-%02d", i)
			g.AddNode(nodes[i])
		}

		// Only add forward edges (i < j) so the graph is guaranteed to be a DAG.
		for i := 0; i < numNodes; i++ {
			for j := i + 1; j < numNodes; j++ {
				if hegel.Draw(ht, hegel.Integers(0, 1)) == 1 {
					g.AddEdge(Edge{From: nodes[i], To: nodes[j], Protocol: "http", Source: "static"})
				}
			}
		}

		require.NoError(ht, g.Validate(), "every DAG must pass Validate()")

		// Any subgraph of a DAG is also a DAG and must pass Validate().
		var subset []string
		for _, n := range nodes {
			if hegel.Draw(ht, hegel.Integers(0, 1)) == 1 {
				subset = append(subset, n)
			}
		}
		sub := g.Subgraph(subset)
		require.NoError(ht, sub.Validate(), "any subgraph of a DAG must pass Validate()")
	})
}

func TestServiceGraph_InjectedCycleAlwaysFails_PBT(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		numNodes := hegel.Draw(ht, hegel.Integers(2, 10))
		nodes := make([]string, numNodes)
		g := NewServiceGraph()
		for i := 0; i < numNodes; i++ {
			nodes[i] = fmt.Sprintf("svc-%02d", i)
			g.AddNode(nodes[i])
		}

		// Create a guaranteed path nodes[0] -> nodes[1] -> ... -> nodes[last]
		for i := 0; i < numNodes-1; i++ {
			g.AddEdge(Edge{From: nodes[i], To: nodes[i+1], Protocol: "http"})
		}

		// Optionally add extra forward edges
		for i := 0; i < numNodes; i++ {
			for j := i + 2; j < numNodes; j++ {
				if hegel.Draw(ht, hegel.Integers(0, 1)) == 1 {
					g.AddEdge(Edge{From: nodes[i], To: nodes[j], Protocol: "http"})
				}
			}
		}

		// Inject back-edge nodes[last] -> nodes[0]
		g.AddEdge(Edge{From: nodes[numNodes-1], To: nodes[0], Protocol: "http"})

		err := g.Validate()
		require.Error(ht, err)
		require.Contains(ht, err.Error(), "cycle detected")
	})
}

func TestServiceGraph_MergeIdempotenceAndTombstones_PBT(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		numNodes := hegel.Draw(ht, hegel.Integers(2, 8))
		nodes := make([]string, numNodes)
		g := NewServiceGraph()
		for i := 0; i < numNodes; i++ {
			nodes[i] = fmt.Sprintf("svc-%02d", i)
			g.AddNode(nodes[i])
		}
		g.AddEntrypoint(nodes[0])

		// Ensure at least one edge exists without adding duplicate (i, j) pairs
		g.AddEdge(Edge{From: nodes[0], To: nodes[1], Protocol: "http", Source: "static"})
		for i := 0; i < numNodes; i++ {
			for j := i + 1; j < numNodes; j++ {
				if i == 0 && j == 1 {
					continue
				}
				if hegel.Draw(ht, hegel.Integers(0, 1)) == 1 {
					g.AddEdge(Edge{From: nodes[i], To: nodes[j], Protocol: "http", Source: "static"})
				}
			}
		}

		origServices := g.Services()
		origEdges := g.Edges()
		origEntrypoints := g.Entrypoints()

		// 1. Idempotence: g.Merge(g) preserves nodes, edges, and entrypoints
		selfMerged := g.Merge(g)
		require.Equal(ht, origServices, selfMerged.Services())
		require.Equal(ht, len(origEdges), len(selfMerged.Edges()))
		require.Equal(ht, origEntrypoints, selfMerged.Entrypoints())

		// 2. Tombstone: merging a graph with a Tombstone edge removes the target edge
		targetEdge := origEdges[0]
		tombstoneGraph := NewServiceGraph()
		tombstoneGraph.edges[targetEdge.From] = append(tombstoneGraph.edges[targetEdge.From], Edge{
			From:      targetEdge.From,
			To:        targetEdge.To,
			Tombstone: true,
		})

		afterTombstone := selfMerged.Merge(tombstoneGraph)
		for _, e := range afterTombstone.Edges() {
			if e.From == targetEdge.From && e.To == targetEdge.To {
				ht.Fatalf("tombstoned edge %s -> %s was not removed by Merge", targetEdge.From, targetEdge.To)
			}
		}
		require.Equal(ht, len(origEdges)-1, len(afterTombstone.Edges()))
	})
}
