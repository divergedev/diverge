package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/divergedev/diverge/pkg/topology"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatMermaid(t *testing.T) {
	g := topology.NewServiceGraph()
	g.AddNode("gateway")
	g.AddNode("api")
	g.AddNode("payments")
	g.AddEntrypoint("gateway")
	g.AddEdge(topology.Edge{From: "gateway", To: "api", Protocol: "http"})
	g.AddEdge(topology.Edge{From: "api", To: "payments", Protocol: "http"})

	var buf bytes.Buffer
	err := RenderMermaid(g, &buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "```mermaid")
	assert.Contains(t, out, "graph LR")
	assert.Contains(t, out, "gateway:::entrypoint")
	assert.Contains(t, out, "gateway --> api")
	assert.Contains(t, out, "api --> payments")
}

func TestFormatDot(t *testing.T) {
	g := topology.NewServiceGraph()
	g.AddNode("gateway")
	g.AddNode("api")
	g.AddNode("payments")
	g.AddEntrypoint("gateway")
	g.AddEdge(topology.Edge{From: "gateway", To: "api", Protocol: "http"})
	g.AddEdge(topology.Edge{From: "api", To: "payments", Protocol: "http"})

	var buf bytes.Buffer
	err := RenderDot(g, &buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "digraph topology")
	assert.Contains(t, out, "rankdir=LR;")
	assert.Contains(t, out, "gateway [shape=doubleoctagon];")
	assert.Contains(t, out, "gateway -> api;")
	assert.Contains(t, out, "api -> payments;")
}

func TestFormatJSON(t *testing.T) {
	g := topology.NewServiceGraph()
	g.AddNode("gateway")
	g.AddNode("api")
	g.AddNode("payments")
	g.AddEntrypoint("gateway")
	g.AddEdge(topology.Edge{From: "gateway", To: "api", Protocol: "http"})
	g.AddEdge(topology.Edge{From: "api", To: "payments", Protocol: "http"})

	var buf bytes.Buffer
	err := RenderJSON(g, &buf)
	require.NoError(t, err)

	var res jsonGraph
	err = json.Unmarshal(buf.Bytes(), &res)
	require.NoError(t, err)

	assert.Contains(t, res.Services, "gateway")
	assert.Contains(t, res.Services, "api")
	assert.Contains(t, res.Services, "payments")

	assert.Contains(t, res.Entrypoints, "gateway")

	assert.Len(t, res.Edges, 2)
	assert.Equal(t, "gateway", res.Edges[0].From)
	assert.Equal(t, "api", res.Edges[0].To)
	assert.Equal(t, "api", res.Edges[1].From)
	assert.Equal(t, "payments", res.Edges[1].To)
}
