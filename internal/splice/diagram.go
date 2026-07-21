package splice

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// diagramMinWidth is the width floor for box rendering. Below it diagrams
// degrade to an indented list instead of broken boxes (the TUI's tiny tier
// starts at 58 columns).
const diagramMinWidth = 58

// diagramLabelMaxRunes caps node and message labels so one long title cannot
// blow out the layout.
const diagramLabelMaxRunes = 28

// TaskGraphFromPlan builds a flow Diagram from the plan's task dependency
// DAG. Edge direction is dependency to dependent so foundations render at
// the top. Cycle and unknown-dependency failures name the offending task
// IDs, mirroring topologicalOrder.
func TaskGraphFromPlan(plan schemas.DesignPlan) (schemas.Diagram, error) {
	if _, err := topologicalOrder(plan.Tasks); err != nil {
		return schemas.Diagram{}, err
	}
	d := schemas.Diagram{
		Kind:  schemas.DiagramKindFlow,
		Title: "Task graph",
		Nodes: make([]schemas.DiagramNode, 0, len(plan.Tasks)),
	}
	for _, task := range plan.Tasks {
		d.Nodes = append(d.Nodes, schemas.DiagramNode{ID: task.ID, Label: task.Title})
		for _, dep := range task.DependsOn {
			d.Edges = append(d.Edges, schemas.DiagramEdge{From: dep, To: task.ID})
		}
	}
	if err := d.Validate(); err != nil {
		return schemas.Diagram{}, fmt.Errorf("task graph from plan: %w", err)
	}
	return d, nil
}

// RenderDiagram draws d as ASCII art capped to width columns. When width is
// too small or the layout would overflow, it degrades to an indented list.
// The output is deterministic: identical input and width give identical
// bytes. The title is not emitted; callers render their own header.
func RenderDiagram(d schemas.Diagram, width int) (string, error) {
	if err := d.Validate(); err != nil {
		return "", err
	}
	if d.Kind == schemas.DiagramKindSequence {
		return renderSequence(d, width), nil
	}
	return renderFlow(d, width)
}

// flowLayers assigns every node a layer (0 = no predecessors, else one more
// than the deepest predecessor) using Kahn's algorithm. Iteration follows
// node declaration order everywhere so the result is deterministic. A cycle
// returns an error naming the unresolved node IDs.
func flowLayers(d schemas.Diagram) ([][]string, error) {
	preds := make(map[string][]string, len(d.Nodes))
	inDegree := make(map[string]int, len(d.Nodes))
	dependents := make(map[string][]string, len(d.Nodes))
	for _, n := range d.Nodes {
		inDegree[n.ID] = 0
	}
	for _, e := range d.Edges {
		inDegree[e.To]++
		preds[e.To] = append(preds[e.To], e.From)
		dependents[e.From] = append(dependents[e.From], e.To)
	}
	layer := make(map[string]int, len(d.Nodes))
	ready := make([]string, 0, len(d.Nodes))
	for _, n := range d.Nodes {
		if inDegree[n.ID] == 0 {
			ready = append(ready, n.ID)
		}
	}
	resolved := 0
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		resolved++
		for _, p := range preds[id] {
			if layer[p]+1 > layer[id] {
				layer[id] = layer[p] + 1
			}
		}
		for _, dependent := range dependents[id] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	if resolved != len(d.Nodes) {
		unresolved := make([]string, 0, len(d.Nodes)-resolved)
		for _, n := range d.Nodes {
			if inDegree[n.ID] > 0 {
				unresolved = append(unresolved, n.ID)
			}
		}
		return nil, fmt.Errorf("dependency cycle among node ids: %s", strings.Join(unresolved, ", "))
	}
	maxLayer := 0
	for _, n := range d.Nodes {
		if layer[n.ID] > maxLayer {
			maxLayer = layer[n.ID]
		}
	}
	layers := make([][]string, maxLayer+1)
	for _, n := range d.Nodes {
		l := layer[n.ID]
		layers[l] = append(layers[l], n.ID)
	}
	return layers, nil
}

// diagramPredecessors maps each node to its predecessor IDs in edge
// declaration order.
func diagramPredecessors(d schemas.Diagram) map[string][]string {
	preds := make(map[string][]string, len(d.Nodes))
	for _, e := range d.Edges {
		preds[e.To] = append(preds[e.To], e.From)
	}
	return preds
}

func runeWidth(s string) int {
	return len([]rune(s))
}

// truncateLabel shortens s to max runes, ending with an ellipsis when cut.
// (context.go already owns truncateRunes, with different semantics.)
func truncateLabel(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max < 2 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// boxLines renders one labeled box. Inner width is the label rune count.
func boxLines(label string) []string {
	w := runeWidth(label)
	return []string{
		"┌" + strings.Repeat("─", w+2) + "┐",
		"│ " + label + " │",
		"└" + strings.Repeat("─", w+2) + "┘",
	}
}

// flowBlock is one rendered layer: lines plus each node's center column
// relative to the block origin. tree marks the stacked-branch fallback form.
type flowBlock struct {
	lines   []string
	centers []int
	width   int
	tree    bool
}

// canvas is a fixed-width rune line builder for connector and sequence rows.
type canvas []rune

func newCanvas(width int) canvas {
	c := make(canvas, width)
	for i := range c {
		c[i] = ' '
	}
	return c
}

func (c canvas) setRune(col int, r rune) {
	if col >= 0 && col < len(c) {
		c[col] = r
	}
}

func (c canvas) writeText(col int, s string) {
	for _, r := range s {
		if col >= len(c) {
			return
		}
		if col >= 0 {
			c[col] = r
		}
		col++
	}
}

func (c canvas) String() string {
	return strings.TrimRight(string(c), " ")
}

// glyphLine renders a row of box-drawing glyphs at the given columns,
// filling spanStart to spanEnd with horizontal rules first.
func glyphLine(marks map[int]rune, spanStart, spanEnd int) string {
	maxCol := 0
	for col := range marks {
		if col > maxCol {
			maxCol = col
		}
	}
	if spanEnd > maxCol {
		maxCol = spanEnd
	}
	line := newCanvas(maxCol + 1)
	for col := spanStart; col <= spanEnd; col++ {
		line.setRune(col, '─')
	}
	for col, r := range marks {
		line.setRune(col, r)
	}
	return line.String()
}

func renderFlow(d schemas.Diagram, width int) (string, error) {
	if width < diagramMinWidth {
		return flowList(d), nil
	}
	layers, err := flowLayers(d)
	if err != nil {
		return "", err
	}
	labels := make(map[string]string, len(d.Nodes))
	for _, n := range d.Nodes {
		labels[n.ID] = truncateLabel(n.Label, diagramLabelMaxRunes)
	}
	preds := diagramPredecessors(d)
	hasEdge := make(map[[2]string]bool, len(d.Edges))
	for _, e := range d.Edges {
		hasEdge[[2]string{e.From, e.To}] = true
	}

	blocks := make([]flowBlock, len(layers))
	contentWidth := 0
	for i, layer := range layers {
		b := buildFlowBlock(layer, labels, width)
		if b.width > contentWidth {
			contentWidth = b.width
		}
		blocks[i] = b
	}
	if contentWidth > width {
		return flowList(d), nil
	}

	pads := make([]int, len(blocks))
	for i, b := range blocks {
		pads[i] = contentWidth/2 - b.width/2
		if pads[i] < 0 {
			pads[i] = 0
		}
	}
	absCenter := func(layerIdx, nodeIdx int) int {
		return pads[layerIdx] + blocks[layerIdx].centers[nodeIdx]
	}

	var out []string
	emit := func(line string) {
		out = append(out, strings.TrimRight(line, " "))
	}

	drawn := make(map[string]bool, len(d.Nodes))
	for i := range blocks {
		for _, line := range blocks[i].lines {
			emit(strings.Repeat(" ", pads[i]) + line)
		}
		if i == len(blocks)-1 {
			break
		}
		above, below := layers[i], layers[i+1]
		switch {
		case len(above) == 1 && len(below) == 1:
			u, v := above[0], below[0]
			if hasEdge[[2]string{u, v}] {
				c := absCenter(i, 0)
				emit(glyphLine(map[int]rune{c: '│'}, 0, -1))
				emit(glyphLine(map[int]rune{c: '▼'}, 0, -1))
				drawn[v] = true
			}
		case len(above) == 1 && len(below) > 1 && !blocks[i+1].tree:
			u := above[0]
			uc := absCenter(i, 0)
			marks := map[int]rune{uc: '┴'}
			spanStart, spanEnd := uc, uc
			arrows := make(map[int]rune)
			for j, v := range below {
				if !hasEdge[[2]string{u, v}] {
					continue
				}
				vc := absCenter(i+1, j)
				if marks[vc] == '┴' {
					marks[vc] = '┼'
				} else {
					marks[vc] = '┬'
				}
				arrows[vc] = '▼'
				if vc < spanStart {
					spanStart = vc
				}
				if vc > spanEnd {
					spanEnd = vc
				}
				drawn[v] = true
			}
			if len(arrows) > 0 {
				emit(glyphLine(map[int]rune{uc: '│'}, 0, -1))
				emit(glyphLine(marks, spanStart, spanEnd))
				emit(glyphLine(arrows, 0, -1))
			}
		case len(above) == 1 && len(below) > 1:
			// Tree children carry their own branch glyphs, so only the drop
			// from the parent is needed.
			u := above[0]
			connected := false
			for _, v := range below {
				if hasEdge[[2]string{u, v}] {
					drawn[v] = true
					connected = true
				}
			}
			if connected {
				emit(glyphLine(map[int]rune{absCenter(i, 0): '│'}, 0, -1))
			}
		case len(above) > 1 && len(below) == 1:
			v := below[0]
			vc := absCenter(i+1, 0)
			for _, u := range above {
				if hasEdge[[2]string{u, v}] {
					emit(glyphLine(map[int]rune{vc: '│'}, 0, -1))
					emit(glyphLine(map[int]rune{vc: '▼'}, 0, -1))
					drawn[v] = true
					break
				}
			}
		default:
			// Many to many: edges are reported through the note lines.
		}
	}

	for _, n := range d.Nodes {
		ps := preds[n.ID]
		if len(ps) == 0 {
			continue
		}
		names := make([]string, 0, len(ps))
		start := 0
		verb := " depends on: "
		if drawn[n.ID] {
			if len(ps) == 1 {
				continue
			}
			start = 1
			verb = " also depends on: "
		}
		for _, p := range ps[start:] {
			names = append(names, labels[p])
		}
		emit("note: " + labels[n.ID] + verb + strings.Join(names, ", "))
	}
	return strings.Join(out, "\n"), nil
}

// buildFlowBlock renders one layer. Multiple nodes render side by side when
// the row fits width, otherwise as stacked branch lines.
func buildFlowBlock(layer []string, labels map[string]string, width int) flowBlock {
	if len(layer) == 1 {
		lines := boxLines(labels[layer[0]])
		return flowBlock{lines: lines, centers: []int{runeWidth(lines[0]) / 2}, width: runeWidth(lines[0])}
	}
	boxes := make([][]string, len(layer))
	rowWidth := 0
	for j, id := range layer {
		boxes[j] = boxLines(labels[id])
		rowWidth += runeWidth(boxes[j][0])
		if j > 0 {
			rowWidth += 2
		}
	}
	if rowWidth <= width {
		b := flowBlock{lines: make([]string, 3), width: rowWidth}
		offset := 0
		for _, box := range boxes {
			for r := 0; r < 3; r++ {
				if offset > 0 {
					b.lines[r] += "  "
				}
				b.lines[r] += box[r]
			}
			b.centers = append(b.centers, offset+runeWidth(box[0])/2)
			offset += runeWidth(box[0]) + 2
		}
		return b
	}
	b := flowBlock{tree: true}
	for j, id := range layer {
		branch := "├─▶ "
		if j == len(layer)-1 {
			branch = "└─▶ "
		}
		line := branch + labels[id]
		b.lines = append(b.lines, line)
		b.centers = append(b.centers, 2)
		if runeWidth(line) > b.width {
			b.width = runeWidth(line)
		}
	}
	return b
}

// flowList renders the narrow or overflow fallback: one indented line per
// node in topological order.
func flowList(d schemas.Diagram) string {
	layers, err := flowLayers(d)
	if err != nil {
		return "diagram unavailable: " + err.Error()
	}
	labels := make(map[string]string, len(d.Nodes))
	for _, n := range d.Nodes {
		labels[n.ID] = truncateLabel(n.Label, diagramLabelMaxRunes)
	}
	preds := diagramPredecessors(d)
	var out []string
	for _, layer := range layers {
		for _, id := range layer {
			line := "  - " + labels[id]
			if ps := preds[id]; len(ps) > 0 {
				names := make([]string, 0, len(ps))
				for _, p := range ps {
					names = append(names, labels[p])
				}
				line += " (depends on: " + strings.Join(names, ", ") + ")"
			}
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func renderSequence(d schemas.Diagram, width int) string {
	n := len(d.Nodes)
	spacing := width / n
	if width < diagramMinWidth || n*12 > width || spacing < 8 {
		return sequenceList(d)
	}
	maxLabel := spacing - 4
	if maxLabel > diagramLabelMaxRunes {
		maxLabel = diagramLabelMaxRunes
	}
	if maxLabel < 3 {
		return sequenceList(d)
	}
	centers := make([]int, n)
	index := make(map[string]int, n)
	labels := make(map[string]string, n)
	for i, node := range d.Nodes {
		centers[i] = spacing*i + spacing/2
		index[node.ID] = i
		labels[node.ID] = node.Label
	}

	lifelines := func() string {
		line := newCanvas(width)
		for _, c := range centers {
			line.setRune(c, '│')
		}
		return line.String()
	}

	top, mid, bot := newCanvas(width), newCanvas(width), newCanvas(width)
	for i, node := range d.Nodes {
		label := truncateLabel(node.Label, maxLabel)
		bw := runeWidth(label) + 4
		left := centers[i] - bw/2
		if left < 0 || left+bw > width {
			return sequenceList(d)
		}
		rule := strings.Repeat("─", bw-2)
		top.writeText(left, "┌"+rule+"┐")
		mid.writeText(left, "│ "+label+" │")
		bot.writeText(left, "└"+rule+"┘")
	}

	out := []string{top.String(), mid.String(), bot.String(), lifelines()}

	msgs := make([]schemas.DiagramEdge, len(d.Edges))
	copy(msgs, d.Edges)
	sort.SliceStable(msgs, func(a, b int) bool { return msgs[a].Order < msgs[b].Order })

	for _, msg := range msgs {
		from := centers[index[msg.From]]
		to := centers[index[msg.To]]
		label := truncateLabel(msg.Label, diagramLabelMaxRunes)
		if label != "" {
			line := newCanvas(width)
			for _, c := range centers {
				line.setRune(c, '│')
			}
			start := from + 1
			avail := 0
			if from == to {
				avail = width - from - 2
			} else {
				if to < from {
					start = to + 1
				}
				avail = absInt(to-from) - 2
			}
			if avail > 0 {
				line.writeText(start, truncateLabel(label, avail))
			}
			out = append(out, line.String())
		}
		arrow := newCanvas(width)
		for _, c := range centers {
			arrow.setRune(c, '│')
		}
		switch {
		case from == to:
			arrow.setRune(from+1, '↻')
		case from < to:
			for c := from + 1; c < to; c++ {
				arrow.setRune(c, '─')
			}
			arrow.setRune(to, '▶')
		default:
			for c := to + 1; c < from; c++ {
				arrow.setRune(c, '─')
			}
			arrow.setRune(to, '◀')
		}
		out = append(out, arrow.String())
	}
	out = append(out, lifelines())
	return strings.Join(out, "\n")
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// sequenceList renders the narrow fallback: one numbered line per message.
func sequenceList(d schemas.Diagram) string {
	labels := make(map[string]string, len(d.Nodes))
	for _, n := range d.Nodes {
		labels[n.ID] = n.Label
	}
	msgs := make([]schemas.DiagramEdge, len(d.Edges))
	copy(msgs, d.Edges)
	sort.SliceStable(msgs, func(a, b int) bool { return msgs[a].Order < msgs[b].Order })
	var out []string
	for _, msg := range msgs {
		line := fmt.Sprintf("  %d. %s -> %s", msg.Order, labels[msg.From], labels[msg.To])
		if msg.Label != "" {
			line += ": " + msg.Label
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
