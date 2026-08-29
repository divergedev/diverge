package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/divergedev/diverge/pkg/topology"
)

type GraphFormat string

const (
	FormatText    GraphFormat = "text"
	FormatMermaid GraphFormat = "mermaid"
	FormatDot     GraphFormat = "dot"
	FormatJSON    GraphFormat = "json"
)

func RenderMermaid(g *topology.ServiceGraph, w io.Writer) error {
	_, err := fmt.Fprintf(w, "```mermaid\ngraph LR\n")
	if err != nil {
		return err
	}

	for _, ep := range g.Entrypoints() {
		_, err = fmt.Fprintf(w, "    %s:::entrypoint\n", ep)
		if err != nil {
			return err
		}
	}

	edges := g.Edges()
	for _, edge := range edges {
		_, err = fmt.Fprintf(w, "    %s --> %s\n", edge.From, edge.To)
		if err != nil {
			return err
		}
	}

	_, err = fmt.Fprintf(w, "```\n")
	return err
}

func RenderDot(g *topology.ServiceGraph, w io.Writer) error {
	_, err := fmt.Fprintf(w, "digraph topology {\n    rankdir=LR;\n")
	if err != nil {
		return err
	}

	for _, ep := range g.Entrypoints() {
		_, err = fmt.Fprintf(w, "    %s [shape=doubleoctagon];\n", ep)
		if err != nil {
			return err
		}
	}

	edges := g.Edges()
	for _, edge := range edges {
		_, err = fmt.Fprintf(w, "    %s -> %s;\n", edge.From, edge.To)
		if err != nil {
			return err
		}
	}

	_, err = fmt.Fprintf(w, "}\n")
	return err
}

type jsonGraph struct {
	Services    []string   `json:"services"`
	Edges       []jsonEdge `json:"edges"`
	Entrypoints []string   `json:"entrypoints"`
}

type jsonEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Protocol string `json:"protocol"`
}

func RenderJSON(g *topology.ServiceGraph, w io.Writer) error {
	res := jsonGraph{
		Services:    g.Services(),
		Entrypoints: g.Entrypoints(),
		Edges:       []jsonEdge{},
	}

	if res.Services == nil {
		res.Services = []string{}
	}
	if res.Entrypoints == nil {
		res.Entrypoints = []string{}
	}

	for _, edge := range g.Edges() {
		protocol := edge.Protocol
		if protocol == "" {
			protocol = "http"
		}
		res.Edges = append(res.Edges, jsonEdge{
			From:     edge.From,
			To:       edge.To,
			Protocol: protocol,
		})
	}

	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}

	_, err = w.Write(append(b, '\n'))
	return err
}
