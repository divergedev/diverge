package topology

import "context"

// StaticConfig represents the topology-relevant portion of .diverge.yaml.
// This avoids importing internal/config (which would create a circular dep).
type StaticConfig struct {
	Services map[string]StaticServiceConfig
}

type StaticServiceConfig struct {
	DependsOn  []string
	Entrypoint bool
}

// StaticDiscoverer builds a ServiceGraph from static .diverge.yaml config.
type StaticDiscoverer struct {
	Config StaticConfig
}

func (d *StaticDiscoverer) Discover(ctx context.Context, namespaces []string) (*ServiceGraph, error) {
	graph := NewServiceGraph()

	for svcName, svcConfig := range d.Config.Services {
		// Register every service as a node so isolated services survive merge
		graph.AddNode(svcName)

		if svcConfig.Entrypoint {
			graph.AddEntrypoint(svcName)
		}

		for _, dep := range svcConfig.DependsOn {
			graph.AddEdge(Edge{
				From:   svcName,
				To:     dep,
				Source: "static",
			})
		}
	}

	return graph, nil
}

func (d *StaticDiscoverer) Name() string { return "static" }
