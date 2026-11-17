package web

import (
	"cmp"
	"math"
	"slices"
	"sort"
	"strconv"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Edge routing is the standard layered-graph method. An edge that spans more
// than one column reserves a slot in each column that it passes: a virtual
// node, a third of a card high. The edge runs straight through its slots and
// bends in the gaps, so it never crosses a card.
const (
	layoutSlotHeight = 36
	// layoutSlotGap is the gap between a slot and the item next to it.
	layoutSlotGap = 12
	// layoutPillHeight is the height of an edge label.
	layoutPillHeight = 16
)

// colItem is one entry of a column: a node card, or the slot of an edge.
type colItem struct {
	node *presentrun.GraphNode // nil for a slot
	edge int                   // the edge of a slot
	// preds and succs are the items in the next column to the left and to
	// the right that an edge segment connects to this item.
	preds, succs []*colItem
	idx          int     // the place in the column
	center       float64 // the sort key of a barycenter pass
	x, y, h      int
}

// colEdge is one dependency edge, with the items it passes.
type colEdge struct {
	from, to string
	payload  string // "" when the payload is not known
	items    []*colItem
}

// buildColumns puts each node in the column of its depth, in graph order,
// and adds the slots of each edge that skips a column, in edge order.
func buildColumns(graph presentrun.Graph) ([][]*colItem, []colEdge) {
	var cols [][]*colItem
	depthCol := map[int]int{}
	nodeItem := map[string]*colItem{}
	nodeCol := map[string]int{}
	for i := range graph.Nodes {
		node := &graph.Nodes[i]
		col, ok := depthCol[node.Depth]
		if !ok {
			col = len(cols)
			depthCol[node.Depth] = col
			cols = append(cols, nil)
		}
		it := &colItem{node: node, h: layoutNodeHeight}
		cols[col] = append(cols[col], it)
		nodeItem[node.Name], nodeCol[node.Name] = it, col
	}
	var edges []colEdge
	for _, node := range graph.Nodes {
		for i, dep := range node.Dependencies {
			if _, ok := nodeItem[dep]; !ok {
				continue
			}
			edge := colEdge{from: dep, to: node.Name}
			if len(node.DependencyPayloads) == len(node.Dependencies) {
				edge.payload = string(node.DependencyPayloads[i])
			}
			edge.items = append(edge.items, nodeItem[dep])
			for c := nodeCol[dep] + 1; c < nodeCol[node.Name]; c++ {
				slot := &colItem{edge: len(edges), h: layoutSlotHeight}
				cols[c] = append(cols[c], slot)
				edge.items = append(edge.items, slot)
			}
			edge.items = append(edge.items, nodeItem[node.Name])
			for k := 1; k < len(edge.items); k++ {
				a, b := edge.items[k-1], edge.items[k]
				a.succs = append(a.succs, b)
				b.preds = append(b.preds, a)
			}
			edges = append(edges, edge)
		}
	}
	for _, col := range cols {
		for i, it := range col {
			it.idx = i
		}
	}
	return cols, edges
}

// orderColumns cuts crossings with one barycenter pass from left to right
// and one back. An item moves to the mean place of its neighbors in the
// column before it (or after it, on the way back). An item with no neighbor
// there keeps its place. The sort is stable, so ties keep graph order, and
// the result is deterministic.
func orderColumns(cols [][]*colItem) {
	sweep := func(c int, neighbors func(*colItem) []*colItem) {
		col := cols[c]
		for _, it := range col {
			it.center = float64(it.idx)
			if near := neighbors(it); len(near) > 0 {
				sum := 0
				for _, n := range near {
					sum += n.idx
				}
				it.center = float64(sum) / float64(len(near))
			}
		}
		// The place in the column breaks ties: the same order as a stable sort.
		slices.SortFunc(col, func(a, b *colItem) int {
			return cmp.Or(cmp.Compare(a.center, b.center), cmp.Compare(a.idx, b.idx))
		})
		for i, it := range col {
			it.idx = i
		}
	}
	for c := 1; c < len(cols); c++ {
		sweep(c, func(it *colItem) []*colItem { return it.preds })
	}
	for c := len(cols) - 2; c >= 0; c-- {
		sweep(c, func(it *colItem) []*colItem { return it.succs })
	}
}

// placeColumns sets the position of each item and returns the height of the
// tallest column. Two cards keep layoutGapY between them; a slot keeps
// layoutSlotGap.
func placeColumns(cols [][]*colItem) int {
	height := func(col []*colItem) int {
		h := 0
		for i, it := range col {
			h += it.h
			if i > 0 {
				h += itemGap(col[i-1], it)
			}
		}
		return h
	}
	tallest := 0
	for _, col := range cols {
		tallest = max(tallest, height(col))
	}
	for c, col := range cols {
		y := layoutPad + (tallest-height(col))/2
		for i, it := range col {
			if i > 0 {
				y += itemGap(col[i-1], it)
			}
			it.x, it.y = layoutPad+c*(layoutNodeWidth+layoutGapX), y
			y += it.h
		}
	}
	return tallest
}

func itemGap(a, b *colItem) int {
	if a.node != nil && b.node != nil {
		return layoutGapY
	}
	return layoutSlotGap
}

// point is a point of a routed edge.
type point struct{ x, y float64 }

// routeEdges draws each edge from the right middle of its dependency,
// through its slots, to the left middle of its node, and places its label.
func routeEdges(edges []colEdge, at map[string]layoutNode, height int) []layoutEdge {
	out := make([]layoutEdge, 0, len(edges))
	// Labels avoid the lines of other edges only while the line index stays
	// inside its budget (maxIndexedSegments). Else they avoid each other only.
	bends := 0
	for _, e := range edges {
		bends += len(e.items) - 1
	}
	var lines lineIndex
	if bends*bendSteps <= maxIndexedSegments {
		lines = lineIndex{}
	}
	for i, e := range edges {
		from := at[e.from]
		x, y := from.X+layoutNodeWidth, from.Y+layoutNodeHeight/2
		lineBottom := y
		path := pathOf('M', x, y)
		bend := func(x2, y2 int) {
			mid := (x + x2) / 2
			lineBottom = max(lineBottom, y2)
			path = appendPath(path, 'C', mid, y, mid, y2, x2, y2)
			if lines != nil {
				lines.addBend(i, point{float64(x), float64(y)}, point{float64(x2), float64(y2)})
			}
			x, y = x2, y2
		}
		for _, slot := range e.items[1 : len(e.items)-1] {
			bend(slot.x, slot.y+layoutSlotHeight/2)
			x = slot.x + layoutNodeWidth
			path = appendPath(path, 'H', x)
		}
		to := at[e.to]
		// The label goes in the last gap, at the middle of the last bend.
		midX, midY := (x+to.X)/2, (y+to.Y+layoutNodeHeight/2)/2
		bend(to.X, to.Y+layoutNodeHeight/2)
		edge := layoutEdge{
			From: e.from, To: e.to, Path: string(path), Payload: e.payload,
			MidX: midX, MidY: midY, LineBottom: lineBottom,
			Flowed: from.Frame.Status == string(presentation.NodeStatusComplete),
		}
		if e.payload != "" {
			edge.Pill = pillWord(schemas.EdgePayload(e.payload))
			// 7 units per character of the 11-unit mono label, and 5 on each side.
			edge.PillWidth = 7*len(edge.Pill) + 10
			edge.PillLeft = -edge.PillWidth / 2
		}
		out = append(out, edge)
	}
	placeLabels(out, lines, height)
	return out
}

// Label placement work is bounded. A label lives in one gap, so the line
// index keeps the sampled bends by gap, and inside a gap by bands of
// lineBand units: a label is tested only against the segments of its gap
// whose band it covers. A layout with more than maxIndexedSegments sampled
// segments makes no index at all, and its labels avoid each other only. A
// complete graph of 20 nodes has about 32,000 segments, and one of 40 nodes
// about 256,000.
const (
	bendSteps          = 24
	lineBand           = 32
	maxIndexedSegments = 100_000
)

// lineSegment is one sampled segment of the bend of an edge.
type lineSegment struct {
	a, b point
	edge int
}

// lineIndex holds the sampled bends by (gap, band).
type lineIndex map[[2]int][]lineSegment

// gapOf is the gap of an x inside a gap: gap g is between column g and g+1.
func gapOf(x float64) int {
	return int(math.Floor((x - layoutPad - layoutNodeWidth) / (layoutNodeWidth + layoutGapX)))
}

func bandOf(y float64) int { return int(math.Floor(y / lineBand)) }

// addBend samples the bend of edge from a to b and indexes its segments.
func (ix lineIndex) addBend(edge int, a, b point) {
	gap, prev := gapOf(a.x), a
	for _, p := range sampleBend(a, b) {
		seg := lineSegment{a: prev, b: p, edge: edge}
		for band := bandOf(min(prev.y, p.y)); band <= bandOf(max(prev.y, p.y)); band++ {
			ix[[2]int{gap, band}] = append(ix[[2]int{gap, band}], seg)
		}
		prev = p
	}
}

// covers reports whether the label of edge i covers a line of another edge
// in its gap.
func (ix lineIndex) covers(e layoutEdge, i int) bool {
	x0, y0, x1, y1 := labelBox(e)
	gap := gapOf(float64(e.MidX))
	for band := bandOf(y0); band <= bandOf(y1); band++ {
		for _, seg := range ix[[2]int{gap, band}] {
			if seg.edge != i && segmentHitsBox(seg.a, seg.b, x0, y0, x1, y1) {
				return true
			}
		}
	}
	return false
}

// pathOf starts a path with a command; appendPath adds " <cmd>n n ...".
// They write what fmt would, without its cost on a large graph.
func pathOf(cmd byte, nums ...int) []byte {
	return appendPath(nil, cmd, nums...)[1:]
}

func appendPath(path []byte, cmd byte, nums ...int) []byte {
	path = append(path, ' ', cmd)
	for k, n := range nums {
		if k > 0 {
			path = append(path, ' ')
		}
		path = strconv.AppendInt(path, int64(n), 10)
	}
	return path
}

// sampleBend returns points along the cubic bend from a to b, without a.
func sampleBend(a, b point) []point {
	const steps = bendSteps
	mid := (a.x + b.x) / 2
	pts := make([]point, 0, steps)
	for k := 1; k <= steps; k++ {
		t := float64(k) / steps
		u := 1 - t
		// Control points (mid, a.y) and (mid, b.y).
		pts = append(pts, point{
			x: u*u*u*a.x + 3*u*u*t*mid + 3*u*t*t*mid + t*t*t*b.x,
			y: u*u*u*a.y + 3*u*u*t*a.y + 3*u*t*t*b.y + t*t*t*b.y,
		})
	}
	return pts
}

// labelBox is the box of an edge label, with a margin of 2 units.
func labelBox(e layoutEdge) (x0, y0, x1, y1 float64) {
	half := float64(e.PillWidth)/2 + 2
	return float64(e.MidX) - half, float64(e.MidY) - layoutPillHeight/2 - 2, float64(e.MidX) + half, float64(e.MidY) + layoutPillHeight/2 + 2
}

// placeLabels moves labels apart. The labels of one gap are placed from the
// top: a label stays at the middle of its last bend when it can. Else it
// moves up or down in steps to the nearest place that covers no other label
// and no other edge, at most 40 units away. When no place is free of edges
// (or lines is nil), it takes the nearest place in that reach that covers no
// other label. When the reach has no such place, it goes to the nearest place
// next to a label of its gap that covers no label (clearOf): an overlap of
// two labels is worse than a label on a line.
//
// The labels of one gap share their x, so two of them overlap exactly when
// their places are less than labelApart apart. Each gap keeps the places of
// its labels sorted, so a test is a binary search.
func placeLabels(edges []layoutEdge, lines lineIndex, height int) {
	order := make([]int, 0, len(edges))
	for i, e := range edges {
		if e.Pill != "" {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		ea, eb := edges[order[a]], edges[order[b]]
		if ea.MidX != eb.MidX {
			return ea.MidX < eb.MidX
		}
		return ea.MidY < eb.MidY
	})
	// A label moves at most 40 units, so it stays next to its own edge.
	const step, reach = 4, 10
	top, bottom := layoutPad+layoutPillHeight/2, height-layoutPad-layoutPillHeight/2
	placedIn := map[int][]int{} // the sorted places of the labels of each gap
	for _, i := range order {
		gap := gapOf(float64(edges[i].MidX))
		ys := placedIn[gap]
		want := edges[i].MidY
		chosen, fallback := -1, -1
		for k := 0; k <= 2*reach && chosen < 0; k++ {
			// 0, +1, -1, +2, -2, ... steps away from the wanted place.
			d := (k + 1) / 2 * step
			if k%2 == 0 {
				d = -d
			}
			y := want + d
			if y < top || y > bottom {
				continue
			}
			edges[i].MidY = y
			if occupied(ys, y) {
				continue
			}
			if fallback < 0 {
				fallback = y
				if lines == nil {
					chosen = y
				}
			}
			if lines != nil && !lines.covers(edges[i], i) {
				chosen = y
			}
		}
		switch {
		case chosen >= 0:
			edges[i].MidY = chosen
		case fallback >= 0:
			edges[i].MidY = fallback
		default:
			edges[i].MidY = clearOf(ys, want, top, bottom)
		}
		placedIn[gap] = slices.Insert(ys, sort.SearchInts(ys, edges[i].MidY), edges[i].MidY)
	}
}

// labelApart is the least distance between the places of two labels of one
// gap that do not overlap: two label boxes with their margins.
const labelApart = layoutPillHeight + 4

// occupied reports whether a label at y overlaps a label at a place of ys.
func occupied(ys []int, y int) bool {
	k := sort.SearchInts(ys, y-labelApart+1)
	return k < len(ys) && ys[k] < y+labelApart
}

// clearOf returns the place nearest to want, labelApart above or below a
// placed label, where a label covers no placed label; on a tie, the higher
// place. ys are the places of the gap, sorted. It prefers a place inside top
// and bottom. Else it takes the nearest free place below bottom, and the
// layout grows to hold it (layoutGraph). It never goes above top, so no label
// leaves the drawn area at the top. It walks the candidates in order once, so
// it is linear in ys.
func clearOf(ys []int, want, top, bottom int) int {
	best, out := want, want
	found, foundOut := false, false
	better := func(c, than int) bool {
		dc, dt := abs(c-want), abs(than-want)
		return dc < dt || dc == dt && c < than
	}
	// The candidates in order: ys[a]-labelApart and ys[b]+labelApart, merged.
	lo := 0 // the first place above c-labelApart
	for a, b := 0, 0; a < len(ys) || b < len(ys); {
		var c int
		if b >= len(ys) || a < len(ys) && ys[a]-labelApart <= ys[b]+labelApart {
			c = ys[a] - labelApart
			a++
		} else {
			c = ys[b] + labelApart
			b++
		}
		for lo < len(ys) && ys[lo] <= c-labelApart {
			lo++
		}
		if lo < len(ys) && ys[lo] < c+labelApart {
			continue // it covers a placed label
		}
		switch {
		case c >= top && c <= bottom:
			if !found || better(c, best) {
				best, found = c, true
			}
		case c > bottom:
			if !foundOut || better(c, out) {
				out, foundOut = c, true
			}
		}
	}
	if found {
		return best
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// segmentHitsBox clips the segment a-b to the box (Liang-Barsky) and reports
// whether a part of it is inside.
func segmentHitsBox(a, b point, x0, y0, x1, y1 float64) bool {
	t0, t1 := 0.0, 1.0
	dx, dy := b.x-a.x, b.y-a.y
	for _, pq := range [4][2]float64{{-dx, a.x - x0}, {dx, x1 - a.x}, {-dy, a.y - y0}, {dy, y1 - a.y}} {
		p, q := pq[0], pq[1]
		if p == 0 {
			if q < 0 {
				return false
			}
			continue
		}
		r := q / p
		if p < 0 {
			if r > t1 {
				return false
			}
			t0 = max(t0, r)
		} else {
			if r < t0 {
				return false
			}
			t1 = min(t1, r)
		}
	}
	return true
}

// layoutRoute is the routed path of one edge, for the editor's Auto-layout.
type layoutRoute struct {
	From string `json:"from"`
	To   string `json:"to"`
	D    string `json:"d"`
	// X and Y are the center of the edge label.
	X int `json:"x"`
	Y int `json:"y"`
	// Bottom is the lowest point of the edge and its label: the editor sizes
	// its canvas to hold it.
	Bottom int `json:"bottom"`
}

func routesOf(layout pipelineLayout) []layoutRoute {
	routes := make([]layoutRoute, 0, len(layout.Edges))
	for _, e := range layout.Edges {
		routes = append(routes, layoutRoute{From: e.From, To: e.To, D: e.Path, X: e.MidX, Y: e.MidY, Bottom: max(e.LineBottom, e.MidY+layoutPillHeight/2)})
	}
	return routes
}
