package schemas

import (
	"errors"
	"fmt"
	"strings"
)

// DiagramKind identifies the layout family of a Diagram.
type DiagramKind string

const (
	DiagramKindFlow     DiagramKind = "flow"
	DiagramKindSequence DiagramKind = "sequence"
)

// DiagramNode is one boxed participant in a Diagram.
type DiagramNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// DiagramEdge is one directed connection between two nodes. Order is only
// meaningful for sequence diagrams, where it drives message ordering.
type DiagramEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
	Order int    `json:"order,omitempty"`
}

// Diagram is the typed, provider-agnostic description of an architecture
// picture. Producers (plan adapters, stage outputs) build it and the
// deterministic renderer draws it. No raw ASCII crosses this boundary.
type Diagram struct {
	Kind  DiagramKind   `json:"kind"`
	Title string        `json:"title"`
	Nodes []DiagramNode `json:"nodes"`
	Edges []DiagramEdge `json:"edges,omitempty"`
}

// Validate checks the diagram structure, naming the offending data.
func (d Diagram) Validate() error {
	switch d.Kind {
	case DiagramKindFlow, DiagramKindSequence:
	default:
		return fmt.Errorf("invalid diagram kind %q", d.Kind)
	}
	if strings.TrimSpace(d.Title) == "" {
		return errors.New("title is required")
	}
	if len(d.Nodes) == 0 {
		return errors.New("at least one node is required")
	}
	ids := make(map[string]struct{}, len(d.Nodes))
	for i, n := range d.Nodes {
		if n.ID == "" {
			return fmt.Errorf("nodes[%d]: id is required", i)
		}
		if strings.TrimSpace(n.Label) == "" {
			return fmt.Errorf("nodes[%d] (%s): label is required", i, n.ID)
		}
		if _, dup := ids[n.ID]; dup {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		ids[n.ID] = struct{}{}
	}
	for i, e := range d.Edges {
		if _, ok := ids[e.From]; !ok {
			return fmt.Errorf("edges[%d]: unknown from node id %q", i, e.From)
		}
		if _, ok := ids[e.To]; !ok {
			return fmt.Errorf("edges[%d]: unknown to node id %q", i, e.To)
		}
	}
	if d.Kind == DiagramKindSequence {
		seen := make(map[int]struct{}, len(d.Edges))
		for i, e := range d.Edges {
			if _, dup := seen[e.Order]; dup {
				return fmt.Errorf("edges[%d]: duplicate sequence order %d", i, e.Order)
			}
			seen[e.Order] = struct{}{}
		}
	}
	return nil
}
