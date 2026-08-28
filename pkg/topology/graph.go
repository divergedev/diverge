package topology

import (
	"fmt"
	"sort"
	"strings"
)

// Edge represents a directed dependency between two services.
type Edge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Protocol  string `json:"protocol,omitempty"`  // http, grpc, async
	Source    string `json:"source,omitempty"`    // gateway-api, prometheus, static
	Tombstone bool   `json:"tombstone,omitempty"` // true = suppress this edge
}

// ServiceGraph is a directed graph of service dependencies.
type ServiceGraph struct {
	edges       map[string][]Edge // adjacency list: from -> []Edge
	reverse     map[string][]Edge // reverse adjacency: to -> []Edge
	entrypoints map[string]bool   // services marked as ingress gateways
	nodes       map[string]bool   // all known service names (including isolated)
}

// IngressPath represents a route from a gateway entrypoint to a target service.
type IngressPath struct {
	Entrypoint string   `json:"entrypoint"`
	Hops       []string `json:"hops"` // ordered: [entrypoint, hop1, ..., target]
}

// NewServiceGraph creates a new ServiceGraph.
func NewServiceGraph() *ServiceGraph {
	return &ServiceGraph{
		edges:       make(map[string][]Edge),
		reverse:     make(map[string][]Edge),
		entrypoints: make(map[string]bool),
		nodes:       make(map[string]bool),
	}
}

// AddEdge adds a directed edge (skips if tombstone).
func (g *ServiceGraph) AddEdge(e Edge) {
	g.nodes[e.From] = true
	g.nodes[e.To] = true
	if e.Tombstone {
		return
	}
	g.edges[e.From] = append(g.edges[e.From], e)
	g.reverse[e.To] = append(g.reverse[e.To], e)
}

// AddNode registers a service name without creating any edges.
// Use for isolated services that should appear in Services().
func (g *ServiceGraph) AddNode(service string) {
	g.nodes[service] = true
}

// AddEntrypoint marks a service as a gateway.
func (g *ServiceGraph) AddEntrypoint(service string) {
	g.entrypoints[service] = true
	g.nodes[service] = true
}

// Edges returns all non-tombstone edges.
func (g *ServiceGraph) Edges() []Edge {
	var allEdges []Edge
	for _, edges := range g.edges {
		for _, e := range edges {
			if !e.Tombstone {
				allEdges = append(allEdges, e)
			}
		}
	}
	return allEdges
}

// Entrypoints returns entrypoint names sorted.
func (g *ServiceGraph) Entrypoints() []string {
	var eps []string
	for ep := range g.entrypoints {
		eps = append(eps, ep)
	}
	sort.Strings(eps)
	return eps
}

// Neighbors returns direct downstream neighbors sorted.
func (g *ServiceGraph) Neighbors(service string) []string {
	var neighbors []string
	seen := make(map[string]bool)
	for _, e := range g.edges[service] {
		if e.Tombstone {
			continue
		}
		if !seen[e.To] {
			seen[e.To] = true
			neighbors = append(neighbors, e.To)
		}
	}
	sort.Strings(neighbors)
	return neighbors
}

// UpstreamCallers returns direct upstream callers sorted.
func (g *ServiceGraph) UpstreamCallers(service string) []string {
	var callers []string
	seen := make(map[string]bool)
	for _, e := range g.reverse[service] {
		if e.Tombstone {
			continue
		}
		if !seen[e.From] {
			seen[e.From] = true
			callers = append(callers, e.From)
		}
	}
	sort.Strings(callers)
	return callers
}

// Validate detects cycles using Tarjan's SCC algorithm. Return error listing the cycle if found.
// Self-loops are NOT errors.
func (g *ServiceGraph) Validate() error {
	var index int
	indices := make(map[string]int)
	lowlink := make(map[string]int)
	onStack := make(map[string]bool)
	var stack []string
	var sccs [][]string

	var strongconnect func(node string)
	strongconnect = func(node string) {
		indices[node] = index
		lowlink[node] = index
		index++
		stack = append(stack, node)
		onStack[node] = true

		for _, neighbor := range g.Neighbors(node) {
			if neighbor == node {
				continue // ignore self-loops
			}
			if _, ok := indices[neighbor]; !ok {
				strongconnect(neighbor)
				if lowlink[neighbor] < lowlink[node] {
					lowlink[node] = lowlink[neighbor]
				}
			} else if onStack[neighbor] {
				if indices[neighbor] < lowlink[node] {
					lowlink[node] = indices[neighbor]
				}
			}
		}

		if lowlink[node] == indices[node] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append([]string{w}, scc...)
				if w == node {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}

	for _, node := range g.Services() {
		if _, ok := indices[node]; !ok {
			strongconnect(node)
		}
	}

	for _, scc := range sccs {
		if len(scc) > 1 {
			// Find cycle in this SCC
			return g.findCycleInSCC(scc)
		}
	}

	return nil
}

func (g *ServiceGraph) findCycleInSCC(scc []string) error {
	inScc := make(map[string]bool)
	for _, n := range scc {
		inScc[n] = true
	}

	start := scc[0]
	visited := make(map[string]bool)
	var path []string

	var dfs func(curr string) bool
	dfs = func(curr string) bool {
		path = append(path, curr)
		visited[curr] = true
		for _, neighbor := range g.Neighbors(curr) {
			if neighbor == curr {
				continue // skip self loop
			}
			if !inScc[neighbor] {
				continue
			}
			if neighbor == start {
				path = append(path, start)
				return true
			}
			if !visited[neighbor] {
				if dfs(neighbor) {
					return true
				}
			}
		}
		path = path[:len(path)-1]
		return false
	}

	if dfs(start) {
		return fmt.Errorf("cycle detected: %s", strings.Join(path, " -> "))
	}
	return fmt.Errorf("cycle detected in components: %v", scc)
}

// AllIngressPaths finds ALL paths from ANY entrypoint to the target service.
// Returns at most maxIngressPaths results to bound computation on dense graphs.
func (g *ServiceGraph) AllIngressPaths(target string) []IngressPath {
	const maxIngressPaths = 100
	var paths []IngressPath

	for _, ep := range g.Entrypoints() {
		if len(paths) >= maxIngressPaths {
			break
		}

		// If the target IS the entrypoint, it's directly reachable
		if ep == target {
			paths = append(paths, IngressPath{
				Entrypoint: ep,
				Hops:       []string{ep},
			})
			continue
		}

		var dfs func(current string, path []string, visited map[string]bool)
		dfs = func(current string, path []string, visited map[string]bool) {
			if len(paths) >= maxIngressPaths {
				return
			}
			path = append(path, current)
			if current == target {
				cp := make([]string, len(path))
				copy(cp, path)
				paths = append(paths, IngressPath{
					Entrypoint: ep,
					Hops:       cp,
				})
				return
			}

			visited[current] = true
			for _, neighbor := range g.Neighbors(current) {
				if !visited[neighbor] {
					dfs(neighbor, path, visited)
				}
			}
			visited[current] = false
		}

		dfs(ep, []string{}, make(map[string]bool))
	}

	return paths
}

// Merge merges two graphs, handling tombstones.
func (g *ServiceGraph) Merge(other *ServiceGraph) *ServiceGraph {
	merged := NewServiceGraph()

	// Build a map of tombstone edges from other
	tombstones := make(map[string]bool)
	for _, edges := range other.edges {
		for _, e := range edges {
			if e.Tombstone {
				key := e.From + "->" + e.To
				tombstones[key] = true
			}
		}
	}

	// Track seen edges to avoid duplicates
	seen := make(map[string]bool)

	// Add edges from g
	for _, e := range g.Edges() {
		key := e.From + "->" + e.To
		if !tombstones[key] && !seen[key] {
			seen[key] = true
			merged.AddEdge(e)
		}
	}

	// Add non-tombstone edges from other
	for _, edges := range other.edges {
		for _, e := range edges {
			if !e.Tombstone {
				key := e.From + "->" + e.To
				if !tombstones[key] && !seen[key] {
					seen[key] = true
					merged.AddEdge(e)
				}
			}
		}
	}

	// Merge entrypoints
	for ep := range g.entrypoints {
		merged.AddEntrypoint(ep)
	}
	for ep := range other.entrypoints {
		merged.AddEntrypoint(ep)
	}

	// Merge nodes (preserves isolated services)
	for n := range g.nodes {
		merged.nodes[n] = true
	}
	for n := range other.nodes {
		merged.nodes[n] = true
	}

	return merged
}

// Services returns a sorted list of all service names.
func (g *ServiceGraph) Services() []string {
	var svcs []string
	for s := range g.nodes {
		svcs = append(svcs, s)
	}
	sort.Strings(svcs)
	return svcs
}

// Subgraph returns a new graph containing only edges where both from and to are in the given service set.
// Selected services that exist in the graph are preserved even if they have no edges.
func (g *ServiceGraph) Subgraph(services []string) *ServiceGraph {
	sub := NewServiceGraph()
	svcMap := make(map[string]bool)
	for _, s := range services {
		svcMap[s] = true
		if g.nodes[s] {
			sub.AddNode(s)
		}
	}

	for _, e := range g.Edges() {
		if svcMap[e.From] && svcMap[e.To] {
			sub.AddEdge(e)
		}
	}

	for ep := range g.entrypoints {
		if svcMap[ep] {
			sub.AddEntrypoint(ep)
		}
	}

	return sub
}
