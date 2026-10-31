package web

import (
	"fmt"
	"hash/fnv"
	"html/template"
	"strings"
	"unicode/utf8"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/presentrun"
)

// The pipeline graph layout is computed on the server, so the first paint is
// static SVG and every browser draws the same picture. Layers run top to
// bottom by dependency depth. Inside a layer the nodes keep the graph order,
// which is declaration order. The layout is a pure function of the graph.
const (
	layoutNodeWidth  = 196
	layoutNodeHeight = 84
	layoutGapX       = 24
	layoutGapY       = 44
	layoutPad        = 12
	// layoutNameRunes caps the drawn name, and layoutLineRunes each smaller
	// line, so no text runs past the box. The full text stays in the title,
	// the accessible label, and the drawer. The editor draws with the same caps.
	layoutNameRunes = 22
	layoutLineRunes = 26
)

// pipelineTemplates holds the page and the graph fragment. html/template
// escapes every text and attribute value.
var pipelineTemplates = template.Must(template.New("pipeline.html").Funcs(template.FuncMap{
	"cut": func(s string) string { return cutRunes(s, layoutLineRunes) },
}).ParseFS(assetFS, "assets/pipeline.html"))

// pipelineLayout is the positioned graph that the SVG template draws.
type pipelineLayout struct {
	// Key changes only when the drawn structure changes: the node names,
	// depths, or edges. A status change keeps the key.
	Key        string
	Width      int
	Height     int
	NodeWidth  int
	NodeHeight int
	// RingWidth and RingHeight size the selection ring, 5 units outside the box.
	RingWidth  int
	RingHeight int
	Nodes      []layoutNode
	Edges      []layoutEdge
}

// layoutNode is one positioned node box.
type layoutNode struct {
	Frame    nodeFrame
	Drawn    string // the name, cut to layoutNameRunes
	Subtitle string // the node type, and "model-free"
	X, Y     int
}

// layoutEdge is one dependency edge, drawn from the dependency to the node.
type layoutEdge struct {
	From, To string
	Path     string
	// Flowed is true when the dependency completed, so its summary moved
	// along this edge.
	Flowed bool
}

// nodeFrame is the live state of one node box. The live stream, the timeline
// steps, and the first paint all use it, so a box looks the same however its
// state arrives. Identity is (Name, Iteration).
//
//   - status: a CSS state token (static, pending, running, complete, failed,
//     degraded, skipped, notrun, removed, stopped).
//   - label: the status words with a glyph, so color is never the only cue.
//   - meta: the token and cost tickers.
type nodeFrame struct {
	Name      string `json:"name"`
	Iteration int    `json:"iteration"`
	Status    string `json:"status"`
	Label     string `json:"label"`
	Meta      string `json:"meta"`
}

// statusMarker is the 3-cell ASCII marker of a state token, the marker that
// the terminal graph shows (presentation.StatusMarker, and "[-]" for a node
// that did not run). The word follows it, so color is never the only cue.
func statusMarker(status string) string {
	switch status {
	case "skipped", "notrun", "removed", "stopped":
		return "[-]"
	}
	return presentation.StatusMarker(presentation.NodeStatus(status), presentation.GlyphTierASCII).Glyph
}

// frameFor projects one graph node onto its box state. It words every fact
// through the presentrun helpers that the terminal view uses.
func frameFor(graph presentrun.Graph, node presentrun.GraphNode) nodeFrame {
	frame := nodeFrame{Name: node.Name, Iteration: node.Iteration}
	if !graph.Live {
		frame.Status = "static"
		frame.Label = "every tier"
		if len(node.SkippedAt) > 0 {
			frame.Label = "skipped at " + strings.Join(node.SkippedAt, ", ")
		}
		return frame
	}
	switch {
	case node.Removed != "":
		frame.Status = "removed"
	case len(node.SkippedAt) > 0:
		frame.Status = "skipped"
	case node.NotRun:
		frame.Status = "notrun"
	case node.Interrupted:
		frame.Status = "stopped"
	case node.Status == "":
		frame.Status = string(presentation.NodeStatusPending)
	default:
		frame.Status = string(node.Status)
	}
	frame.Label = statusMarker(frame.Status) + " " + presentrun.StatusText(node)
	var meta []string
	if node.Tokens > 0 {
		meta = append(meta, presentrun.HumanCount(int(node.Tokens))+" tok")
	}
	if cost := presentrun.CostText(node); cost != "" {
		meta = append(meta, cost)
	}
	frame.Meta = strings.Join(meta, " · ")
	return frame
}

// layoutGraph positions the graph. Each depth is one row, centered in the
// widest row. Edges are vertical cubic curves from the bottom of the
// dependency to the top of the node.
//
// ponytail: edges are not routed around the boxes; an edge that skips a
// layer can cross a box in between. Route through the gaps if real
// topologies show it.
func layoutGraph(graph presentrun.Graph) pipelineLayout {
	var rows [][]presentrun.GraphNode
	depthRow := map[int]int{}
	for _, node := range graph.Nodes {
		row, ok := depthRow[node.Depth]
		if !ok {
			row = len(rows)
			depthRow[node.Depth] = row
			rows = append(rows, nil)
		}
		rows[row] = append(rows[row], node)
	}
	widest := 0
	for _, row := range rows {
		widest = max(widest, len(row))
	}
	rowWidth := func(n int) int { return n*layoutNodeWidth + max(n-1, 0)*layoutGapX }
	out := pipelineLayout{
		NodeWidth:  layoutNodeWidth,
		NodeHeight: layoutNodeHeight,
		RingWidth:  layoutNodeWidth + 10,
		RingHeight: layoutNodeHeight + 10,
		Width:      2*layoutPad + rowWidth(widest),
		Height:     2*layoutPad + len(rows)*layoutNodeHeight + max(len(rows)-1, 0)*layoutGapY,
	}
	key := fnv.New64a()
	at := map[string]layoutNode{}
	for r, row := range rows {
		x := layoutPad + (rowWidth(widest)-rowWidth(len(row)))/2
		y := layoutPad + r*(layoutNodeHeight+layoutGapY)
		for _, node := range row {
			box := layoutNode{
				Frame:    frameFor(graph, node),
				Drawn:    cutRunes(node.Name, layoutNameRunes),
				Subtitle: nodeSubtitle(node),
				X:        x,
				Y:        y,
			}
			out.Nodes = append(out.Nodes, box)
			at[node.Name] = box
			fmt.Fprintf(key, "%s\x00%d\x00%s\x01", node.Name, node.Depth, strings.Join(node.Dependencies, "\x00"))
			x += layoutNodeWidth + layoutGapX
		}
	}
	for _, box := range out.Nodes {
		for _, dep := range graphNode(graph, box.Frame.Name).Dependencies {
			from, ok := at[dep]
			if !ok {
				continue
			}
			x1, y1 := from.X+layoutNodeWidth/2, from.Y+layoutNodeHeight
			x2, y2 := box.X+layoutNodeWidth/2, box.Y
			bend := (y2 - y1) / 2
			out.Edges = append(out.Edges, layoutEdge{
				From:   dep,
				To:     box.Frame.Name,
				Path:   fmt.Sprintf("M%d %d C%d %d %d %d %d %d", x1, y1, x1, y1+bend, x2, y2-bend, x2, y2),
				Flowed: from.Frame.Status == string(presentation.NodeStatusComplete),
			})
		}
	}
	out.Key = fmt.Sprintf("%016x", key.Sum64())
	return out
}

// graphNode returns the named node of the graph.
func graphNode(graph presentrun.Graph, name string) presentrun.GraphNode {
	for _, node := range graph.Nodes {
		if node.Name == name {
			return node
		}
	}
	return presentrun.GraphNode{}
}

// nodeSubtitle is the second line of a box: the node type when it is not the
// name, else the kind, and "model-free" for a node that never calls a model.
func nodeSubtitle(node presentrun.GraphNode) string {
	sub := strings.ToLower(string(node.Kind))
	if node.Type != "" && node.Type != node.Name {
		sub = node.Type
	}
	if node.ModelFree {
		sub += " · model-free"
	}
	return sub
}

// cutRunes shortens s to n runes, with an ellipsis when it cuts.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
