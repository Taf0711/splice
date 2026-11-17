package web

import (
	"fmt"
	"hash/fnv"
	"html/template"
	"strings"
	"unicode"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// The pipeline graph layout is computed on the server, so the first paint is
// static SVG and every browser draws the same picture. Columns run left to
// right by dependency depth. Inside a column the nodes keep the graph order,
// which is declaration order, from top to bottom. The layout is a pure
// function of the graph.
//
// A node box is a card: a header band with the name and the status marker,
// then four lines: the type, the status, what the node receives, and the
// meta line. The editor draws the same card (editor.js), so change the sizes
// here and there together; TestScriptsUseTheLayoutConstants pairs them.
const (
	layoutNodeWidth  = 240
	layoutNodeHeight = 104
	// layoutBandHeight is the height of the header band.
	layoutBandHeight = 26
	// layoutGapX is the gap between two columns. It holds the payload label
	// of an edge. layoutGapY is the gap between two nodes in one column.
	layoutGapX = 110
	layoutGapY = 32
	layoutPad  = 12
	// layoutNameColumns caps the drawn name, and layoutLineColumns each
	// smaller line, in text columns (textColumns), so no text runs past the
	// box. The name has 186 units beside the marker; at 14 units bold, a
	// column is about 8 units on average, and the cap leaves a margin. A wide
	// rune is two columns and 14 units. A clip at layoutNameClip stops a name
	// of only wide Latin letters at the marker. The full text stays in the
	// title, the accessible label, and the drawer. The editor draws with the
	// same caps (cutMiddle and cutEnd in editor.js).
	layoutNameColumns = 21
	layoutLineColumns = 30
	layoutNameClip    = layoutNodeWidth - 36
)

// receivesNothing is the receives line of a node with no dependency.
const receivesNothing = "receives nothing, runs first"

// pipelineTemplates holds the page and the graph fragment. html/template
// escapes every text and attribute value.
var pipelineTemplates = template.Must(template.New("pipeline.html").Funcs(template.FuncMap{
	"cut":   func(s string) string { return cutEnd(s, layoutLineColumns) },
	"mark":  labelMarker,
	"words": labelWords,
}).ParseFS(assetFS, "assets/pipeline.html"))

// pipelineLayout is the positioned graph that the SVG template draws.
type pipelineLayout struct {
	// Key changes only when the drawn structure changes: the node names,
	// depths, edges, or edge payloads. A status change keeps the key.
	Key        string
	Width      int
	Height     int
	NodeWidth  int
	NodeHeight int
	// RingWidth and RingHeight size the selection ring, 5 units outside the box.
	RingWidth  int
	RingHeight int
	// Band is the path of the header band, and MarkX the right end of the
	// status marker in it. NameClip is the width that the name may use.
	Band     string
	MarkX    int
	NameClip int
	Nodes    []layoutNode
	Edges    []layoutEdge
}

// layoutNode is one positioned node box.
type layoutNode struct {
	Frame    nodeFrame
	Drawn    string // the name, cut in the middle to layoutNameColumns
	Subtitle string // the node type, and "model-free"
	Receives string // what the incoming edges carry (receivesText)
	X, Y     int
}

// layoutEdge is one dependency edge, drawn from the dependency to the node.
type layoutEdge struct {
	From, To string
	Path     string
	// Payload is the payload of the topology edge: summary, output, or
	// none. It is empty when the graph has no topology edges, and then the
	// edge has no label.
	Payload string
	// Pill is the label at the middle of the edge, at (MidX, MidY).
	// PillLeft and PillWidth size its box around that point.
	Pill                string
	MidX, MidY          int
	PillLeft, PillWidth int
	// Flowed is true when the dependency completed, so its summary moved
	// along this edge.
	Flowed bool
	// LineBottom is the lowest point of the path. A bend has horizontal ends,
	// so no point of it is lower than its lower end.
	LineBottom int
}

// pillWord is the edge label of a payload. None moves no data, so its label
// says that the edge only sets the order.
func pillWord(payload schemas.EdgePayload) string {
	if payload == schemas.EdgePayloadNone {
		return "order"
	}
	return string(payload)
}

// receivesText says what the incoming edges of a node carry, for example
// "receives summary x2, output". A node whose dependencies have no known
// payload, such as a node of a run plan only, says how many nodes it waits
// for. editor.js words the line with the same rules (receives).
func receivesText(node presentrun.GraphNode) string {
	if len(node.Dependencies) == 0 {
		return receivesNothing
	}
	if len(node.DependencyPayloads) != len(node.Dependencies) {
		if len(node.Dependencies) == 1 {
			return "runs after 1 node"
		}
		return fmt.Sprintf("runs after %d nodes", len(node.Dependencies))
	}
	count := map[string]int{}
	for _, payload := range node.DependencyPayloads {
		count[pillWord(payload)]++
	}
	var parts []string
	for _, word := range []string{"summary", "output", "order"} {
		switch n := count[word]; {
		case n == 1:
			parts = append(parts, word)
		case n > 1:
			parts = append(parts, fmt.Sprintf("%s x%d", word, n))
		}
	}
	return "receives " + strings.Join(parts, ", ")
}

// labelMarker returns the 3-cell marker that starts a status label, such as
// "[+]", or "" when the label has none. The header band shows it.
func labelMarker(label string) string {
	if r := []rune(label); len(r) >= 3 && r[0] == '[' && r[2] == ']' {
		return string(r[:3])
	}
	return ""
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

// layoutGraph positions the graph. Each depth is one column, and the columns
// run left to right. An edge that skips a column gets a slot in each column
// that it passes, so no edge runs through a card. Inside a column the nodes
// and slots are ordered to cut crossings (orderColumns), and each column is
// centered vertically on the tallest column. Edges are horizontal runs
// through their slots, with cubic bends in the gaps (routeEdges).
func layoutGraph(graph presentrun.Graph) pipelineLayout {
	out := pipelineLayout{
		NodeWidth:  layoutNodeWidth,
		NodeHeight: layoutNodeHeight,
		RingWidth:  layoutNodeWidth + 10,
		RingHeight: layoutNodeHeight + 10,
		Band:       fmt.Sprintf("M1 %d V8 Q1 1 8 1 H%d Q%d 1 %d 8 V%d Z", layoutBandHeight, layoutNodeWidth-8, layoutNodeWidth-1, layoutNodeWidth-1, layoutBandHeight),
		MarkX:      layoutNodeWidth - 12,
		NameClip:   layoutNameClip,
	}
	cols, edges := buildColumns(graph)
	orderColumns(cols)
	out.Width = 2*layoutPad + len(cols)*layoutNodeWidth + max(len(cols)-1, 0)*layoutGapX
	out.Height = 2*layoutPad + placeColumns(cols)
	key := fnv.New64a()
	at := map[string]layoutNode{}
	for _, col := range cols {
		for _, it := range col {
			if it.node == nil {
				continue
			}
			node := *it.node
			box := layoutNode{
				Frame:    frameFor(graph, node),
				Drawn:    cutMiddle(node.Name, layoutNameColumns),
				Subtitle: nodeSubtitle(node),
				Receives: receivesText(node),
				X:        it.x,
				Y:        it.y,
			}
			out.Nodes = append(out.Nodes, box)
			at[node.Name] = box
			fmt.Fprintf(key, "%s\x00%d\x00%s\x00%v\x01", node.Name, node.Depth, strings.Join(node.Dependencies, "\x00"), node.DependencyPayloads)
		}
	}
	out.Edges = routeEdges(edges, at, out.Height)
	// A crowded gap can put a label below the columns (clearOf): grow the
	// graph to hold it. A label never goes above the top, and it sits in the
	// middle of a gap, between two columns, so it never leaves at a side.
	for _, e := range out.Edges {
		if e.Pill != "" {
			out.Height = max(out.Height, e.MidY+layoutPillHeight/2+layoutPad)
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

// wideRanges are the East Asian wide and fullwidth runes: two columns each.
// editor.js and pipeline.js hold the same list (WIDE).
var wideRanges = [][2]rune{
	{0x1100, 0x115F}, {0x2E80, 0x303E}, {0x3041, 0x33FF}, {0x3400, 0x4DBF}, {0x4E00, 0x9FFF},
	{0xA000, 0xA4CF}, {0xAC00, 0xD7A3}, {0xF900, 0xFAFF}, {0xFE30, 0xFE4F}, {0xFF00, 0xFF60},
	{0xFFE0, 0xFFE6}, {0x1F300, 0x1F64F}, {0x1F900, 0x1F9FF}, {0x20000, 0x3FFFD},
}

// runeColumns is the width of a rune in text columns: 0 for a combining
// mark, 2 for a wide rune, else 1.
func runeColumns(r rune) int {
	if unicode.In(r, unicode.Mn, unicode.Me) {
		return 0
	}
	for _, w := range wideRanges {
		if r >= w[0] && r <= w[1] {
			return 2
		}
	}
	return 1
}

func textColumns(s string) int {
	n := 0
	for _, r := range s {
		n += runeColumns(r)
	}
	return n
}

// takeColumns returns how many runes from the start of r fit in n columns.
// The combining marks of the last rune that fits come with it.
func takeColumns(r []rune, n int) int {
	k, cols := 0, 0
	for k < len(r) && cols+runeColumns(r[k]) <= n {
		cols += runeColumns(r[k])
		k++
	}
	return k
}

// cutEnd shortens s to n columns, with an ellipsis at the end when it cuts.
func cutEnd(s string, n int) string {
	if textColumns(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:takeColumns(r, n-1)]) + "…"
}

// cutMiddle shortens s to n columns. It keeps the start and the end, with
// one ellipsis in the middle, so two names that differ at the end stay apart.
func cutMiddle(s string, n int) string {
	if textColumns(s) <= n {
		return s
	}
	r := []rune(s)
	budget := max(n-1, 0)
	head := takeColumns(r, (budget+1)/2)
	tail, cols := len(r), 0
	for tail > head && cols+runeColumns(r[tail-1]) <= budget/2 {
		cols += runeColumns(r[tail-1])
		tail--
	}
	// A combining mark whose base was cut goes too.
	for tail < len(r) && runeColumns(r[tail]) == 0 {
		tail++
	}
	return string(r[:head]) + "…" + string(r[tail:])
}

// labelWords is a status label without its marker: the marker shows in the
// header band, so the status line shows only the words.
func labelWords(label string) string {
	if m := labelMarker(label); m != "" {
		return strings.TrimPrefix(strings.TrimPrefix(label, m), " ")
	}
	return label
}
