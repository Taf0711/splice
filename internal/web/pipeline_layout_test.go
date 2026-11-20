package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// pipelineDiamond is one writer, two parallel checks (one a command node), and
// a runner that waits for both. The runner gets the output of lint, and only
// the order of the analyzer, so the graph shows each payload.
func pipelineDiamond() *schemas.PipelineTopology {
	return &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "diamond",
		Nodes: []schemas.PipelineNode{
			{Name: "code_writer", Type: "code_writer"},
			{Name: "lint", Type: schemas.NodeTypeCommand, Command: []string{"go", "vet", "./..."}},
			{Name: "static_analyzer", Type: "static_analyzer"},
			{Name: "test_runner", Type: "test_runner"},
		},
		Edges: []schemas.PipelineEdge{
			{From: "code_writer", To: "lint"},
			{From: "code_writer", To: "static_analyzer"},
			{From: "lint", To: "test_runner", Payload: schemas.EdgePayloadOutput},
			{From: "static_analyzer", To: "test_runner", Payload: schemas.EdgePayloadNone},
		},
	}
}

// pipelineWorkspace writes the diamond as a trusted project topology, so the
// view resolves it without the real user config.
func pipelineWorkspace(t *testing.T) splicerun.TopologySources {
	t.Helper()
	root := t.TempDir()
	data, err := json.Marshal(pipelineDiamond())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".splice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".splice", "pipeline.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return splicerun.TopologySources{WorkspaceRoot: root, Trusted: true, UserConfigDir: t.TempDir()}
}

// pipelineRun drives real plan, stage, and usage events through the reducer.
// The writer passed, lint failed, and the others wait. extra events follow.
func pipelineRun(t *testing.T, extra ...presentation.StreamEventLike) presentation.State {
	t.Helper()
	compiled, err := splicerun.CompileTopology(pipelineDiamond(), schemas.TierStandard)
	if err != nil {
		t.Fatal(err)
	}
	plan := agent.PipelinePlanEvent{Dependencies: map[string][]string{}}
	for _, stage := range compiled.Stages {
		plan.Stages = append(plan.Stages, stage.Name)
		if len(stage.DependsOn) > 0 {
			plan.Dependencies[stage.Name] = stage.DependsOn
		}
	}
	cost := 0.0123
	events := append([]presentation.StreamEventLike{
		presentrun.AdaptPipelinePlan(schemas.ExecutionPlan{RequestIntent: "x", Tier: schemas.TierStandard, TopologyName: "diamond"}, plan),
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "code_writer", Status: "running"}),
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "code_writer", Status: "completed", Detail: "wrote hello.go", Progress: 100}),
		presentation.UsageEvent{NodeID: "code_writer", Usage: presentation.TokenUsage{InputTokens: 12000, OutputTokens: 1800}, CostUSD: &cost},
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "lint", Status: "running"}),
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "lint", Status: "failed", Detail: "check failed: exit status 1"}),
	}, extra...)
	var state presentation.State
	for _, event := range events {
		if state, err = presentation.Apply(state, event); err != nil {
			t.Fatal(err)
		}
	}
	return state
}

func renderGraph(t *testing.T, layout pipelineLayout) string {
	t.Helper()
	var out strings.Builder
	if err := pipelineTemplates.ExecuteTemplate(&out, "graph", layout); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// TestPipelineGraphGolden pins the SVG of a live diamond run, and of the
// static worst case, whose edges skip columns.
func TestPipelineGraphGolden(t *testing.T) {
	worst, err := presentrun.BuildGraph(pipelineWorstCase(), presentation.State{})
	if err != nil {
		t.Fatal(err)
	}
	diamond, err := presentrun.BuildGraph(pipelineDiamond(), pipelineRun(t))
	if err != nil {
		t.Fatal(err)
	}
	for file, graph := range map[string]presentrun.Graph{"pipeline_graph_diamond.golden": diamond, "pipeline_graph_worst.golden": worst} {
		got := []byte(renderGraph(t, layoutGraph(graph)) + "\n")
		path := filepath.Join("testdata", file)
		if os.Getenv("SPLICE_UPDATE_GOLDEN") == "1" {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read golden: %v (create it with SPLICE_UPDATE_GOLDEN=1)", err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("%s mismatch (regenerate with SPLICE_UPDATE_GOLDEN=1):\n--- want\n%s--- got\n%s", file, want, got)
		}
	}
}

// TestPipelineLayoutIsDeterministic pins the layout rules: the same graph
// gives the same bytes, a depth is one column, a column keeps the graph order
// from top to bottom, and the key follows the structure, not the status.
func TestPipelineLayoutIsDeterministic(t *testing.T) {
	build := func(state presentation.State) presentrun.Graph {
		graph, err := presentrun.BuildGraph(pipelineDiamond(), state)
		if err != nil {
			t.Fatal(err)
		}
		return graph
	}
	first := build(pipelineRun(t))
	want := renderGraph(t, layoutGraph(first))
	for i := 0; i < 20; i++ {
		if got := renderGraph(t, layoutGraph(build(pipelineRun(t)))); got != want {
			t.Fatalf("render %d differs from the first render", i)
		}
	}

	layout := layoutGraph(first)
	at := map[string]layoutNode{}
	for _, node := range layout.Nodes {
		at[node.Frame.Name] = node
	}
	lint, analyzer := at["lint"], at["static_analyzer"]
	if lint.X != analyzer.X || lint.Y+layoutNodeHeight > analyzer.Y {
		t.Fatalf("depth-1 column: lint at (%d,%d), static_analyzer at (%d,%d); want one column in declaration order, no overlap", lint.X, lint.Y, analyzer.X, analyzer.Y)
	}
	if !(at["code_writer"].X+layoutNodeWidth < lint.X && lint.X+layoutNodeWidth < at["test_runner"].X) {
		t.Fatal("columns must run left to right by depth, with a gap")
	}
	// A column is centered vertically on the tallest column.
	center := func(n layoutNode) int { return n.Y + layoutNodeHeight/2 }
	if mid := (lint.Y + analyzer.Y + layoutNodeHeight) / 2; center(at["code_writer"]) != mid || center(at["test_runner"]) != mid {
		t.Fatalf("one-node columns at y %d and %d, want centered on %d", center(at["code_writer"]), center(at["test_runner"]), mid)
	}
	if len(layout.Edges) != 4 {
		t.Fatalf("edges = %d, want 4", len(layout.Edges))
	}
	// An edge leaves the right middle of the dependency and enters the left
	// middle of the node.
	for _, edge := range layout.Edges {
		var x1, y1, c1x, c1y, c2x, c2y, x2, y2 int
		if _, err := fmt.Sscanf(edge.Path, "M%d %d C%d %d %d %d %d %d", &x1, &y1, &c1x, &c1y, &c2x, &c2y, &x2, &y2); err != nil {
			t.Fatalf("edge %s -> %s path %q: %v", edge.From, edge.To, edge.Path, err)
		}
		from, to := at[edge.From], at[edge.To]
		if x1 != from.X+layoutNodeWidth || y1 != center(from) || x2 != to.X || y2 != center(to) {
			t.Errorf("edge %s -> %s = %q, want from the right middle to the left middle", edge.From, edge.To, edge.Path)
		}
		if c1y != y1 || c2y != y2 || c1x != c2x || c1x <= x1 || c1x >= x2 {
			t.Errorf("edge %s -> %s = %q, want a horizontal curve", edge.From, edge.To, edge.Path)
		}
	}

	later := layoutGraph(build(pipelineRun(t, presentrun.AdaptStageEvent(agent.StageEvent{Name: "static_analyzer", Status: "running"}))))
	if later.Key != layout.Key {
		t.Fatal("a status change changed the layout key")
	}
	static := layoutGraph(build(presentation.State{}))
	changed := pipelineDiamond()
	changed.Edges = changed.Edges[:3]
	graph, err := presentrun.BuildGraph(changed, presentation.State{})
	if err != nil {
		t.Fatal(err)
	}
	if layoutGraph(graph).Key == static.Key {
		t.Fatal("removing an edge kept the layout key")
	}
}

// TestNodeLinesStayInsideTheBox pins the shared rendering: a long line is
// cut with an ellipsis inside the box, and the title keeps the full text.
func TestNodeLinesStayInsideTheBox(t *testing.T) {
	top := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion, Name: "cut",
		Nodes: []schemas.PipelineNode{{Name: "security_auditor", Type: "security_auditor", Tiers: []schemas.PipelineTier{schemas.TierSubstantial, schemas.TierArchitectural}}},
	}
	graph, err := presentrun.BuildGraph(top, presentation.State{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := pipelineTemplates.ExecuteTemplate(&out, "graph", layoutGraph(graph)); err != nil {
		t.Fatal(err)
	}
	full := "skipped at trivial, light, standard"
	svg := out.String()
	if !strings.Contains(svg, `<text class="n-status" x="14" y="61">`+cutEnd(full, layoutLineColumns)+`</text>`) || !strings.Contains(cutEnd(full, layoutLineColumns), "…") {
		t.Fatalf("the status line is not cut in the box:\n%s", svg)
	}
	if !strings.Contains(svg, "\n"+full+"\n") {
		t.Fatalf("the title lacks the full line:\n%s", svg)
	}
}

// TestScriptsUseTheLayoutConstants pairs the box size and the line caps of
// the two scripts with the server layout, so the editor and the viewer draw
// one box. A change on one side fails here until the other side follows.
func TestScriptsUseTheLayoutConstants(t *testing.T) {
	read := func(path string) string {
		data, err := assetFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	editor, viewer := read("assets/editor.js"), read("assets/pipeline.js")
	for _, want := range []string{
		fmt.Sprintf("const NW = %d, NH = %d, PAD = %d, GAPX = %d, GAPY = %d;", layoutNodeWidth, layoutNodeHeight, layoutPad, layoutGapX, layoutGapY),
		fmt.Sprintf("const BAND = %q;", layoutGraph(presentrun.Graph{}).Band),
		fmt.Sprintf("return %q;", receivesNothing),
		fmt.Sprintf("cutEnd(recv, %d)", layoutLineColumns),
		fmt.Sprintf("cutMiddle(name, %d)", layoutNameColumns),
		fmt.Sprintf("cutEnd(subtitle(n), %d)", layoutLineColumns),
		fmt.Sprintf("cutEnd(status, %d)", layoutLineColumns),
	} {
		if !strings.Contains(editor, want) {
			t.Errorf("editor.js lacks %q", want)
		}
	}
	var wide []string
	for _, w := range wideRanges {
		wide = append(wide, fmt.Sprintf("[0x%x, 0x%x]", w[0], w[1]))
	}
	for _, want := range []string{fmt.Sprintf("cutEnd(words(f.label), %d)", layoutLineColumns), "WIDE = [" + strings.Join(wide, ", ") + "];"} {
		if !strings.Contains(viewer, want) {
			t.Errorf("pipeline.js lacks %q", want)
		}
		if strings.HasPrefix(want, "WIDE") && !strings.Contains(editor, want) {
			t.Errorf("editor.js lacks %q", want)
		}
	}
	if want := fmt.Sprintf(`<clipPath id="name-clip"><rect width="%d" height="26"/>`, layoutNameClip); !strings.Contains(read("assets/editor.html"), want) {
		t.Errorf("editor.html lacks %q", want)
	}
}

// pipelineWorstCase is a hard graph for the layout: nine nodes in six
// columns, fifteen edges, edges that skip columns, long names that differ only
// at the end, a one-letter name, and a non-Latin name.
func pipelineWorstCase() *schemas.PipelineTopology {
	node := func(name string) schemas.PipelineNode {
		return schemas.PipelineNode{Name: name, Type: schemas.NodeTypePrompt, Prompt: "x"}
	}
	edge := func(from, to string, payload schemas.EdgePayload) schemas.PipelineEdge {
		return schemas.PipelineEdge{From: from, To: to, Payload: payload}
	}
	long, longer := "integration_contract_verification_for_billing", "integration_contract_verification_for_billinh"
	return &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "worst-case-pipeline-with-a-very-long-name-that-goes-on-and-on-x",
		Nodes:   []schemas.PipelineNode{node(long), node("t"), node("コードレビュー担当ノード"), node("lint"), node("build"), node("test"), node("security"), node(longer), node("report")},
		Edges: []schemas.PipelineEdge{
			edge(long, "コードレビュー担当ノード", ""), edge(long, "lint", schemas.EdgePayloadOutput), edge("t", "lint", schemas.EdgePayloadNone),
			edge("コードレビュー担当ノード", "build", ""), edge("lint", "build", schemas.EdgePayloadNone),
			edge("build", "test", ""), edge("t", "test", schemas.EdgePayloadOutput),
			edge(long, "security", ""), edge("build", "security", schemas.EdgePayloadNone),
			edge("test", longer, ""), edge("security", longer, schemas.EdgePayloadOutput), edge("lint", longer, ""),
			edge(longer, "report", ""), edge(long, "report", schemas.EdgePayloadNone), edge("t", "report", ""),
		},
	}
}

// pathPoints samples an edge path of the layout: M, then C bends and H runs.
func pathPoints(t *testing.T, path string) [][2]float64 {
	t.Helper()
	fields := strings.Fields(strings.NewReplacer("M", " M ", "C", " C ", "H", " H ").Replace(path))
	num := func(i int) float64 {
		var v float64
		if _, err := fmt.Sscan(fields[i], &v); err != nil {
			t.Fatalf("path %q: %v", path, err)
		}
		return v
	}
	var pts [][2]float64
	var x, y float64
	for i := 0; i < len(fields); {
		switch fields[i] {
		case "M":
			x, y = num(i+1), num(i+2)
			pts = append(pts, [2]float64{x, y})
			i += 3
		case "H":
			for k := 0.0; k <= 1; k += 0.05 {
				pts = append(pts, [2]float64{x + (num(i+1)-x)*k, y})
			}
			x = num(i + 1)
			i += 2
		case "C":
			c1x, c1y, c2x, c2y, ex, ey := num(i+1), num(i+2), num(i+3), num(i+4), num(i+5), num(i+6)
			for k := 0.0; k <= 1; k += 0.01 {
				u := 1 - k
				pts = append(pts, [2]float64{
					u*u*u*x + 3*u*u*k*c1x + 3*u*k*k*c2x + k*k*k*ex,
					u*u*u*y + 3*u*u*k*c1y + 3*u*k*k*c2y + k*k*k*ey,
				})
			}
			x, y = ex, ey
			i += 7
		default:
			t.Fatalf("path %q: unexpected %q", path, fields[i])
		}
	}
	return pts
}

// TestEdgesNeverCrossACard pins the routing: no point of an edge is inside
// the card of a node that is not one of its ends.
func TestEdgesNeverCrossACard(t *testing.T) {
	for _, tc := range []struct {
		name     string
		topology *schemas.PipelineTopology
	}{
		{"diamond", pipelineDiamond()},
		{"default", splicerun.DefaultTopology()},
		{"worst case", pipelineWorstCase()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph, err := presentrun.BuildGraph(tc.topology, presentation.State{})
			if err != nil {
				t.Fatal(err)
			}
			layout := layoutGraph(graph)
			if len(layout.Edges) != len(tc.topology.Edges) {
				t.Fatalf("edges = %d, want %d", len(layout.Edges), len(tc.topology.Edges))
			}
			for _, edge := range layout.Edges {
				for _, p := range pathPoints(t, edge.Path) {
					for _, box := range layout.Nodes {
						if box.Frame.Name == edge.From || box.Frame.Name == edge.To {
							continue
						}
						if p[0] > float64(box.X)+0.5 && p[0] < float64(box.X+layoutNodeWidth)-0.5 && p[1] > float64(box.Y)+0.5 && p[1] < float64(box.Y+layoutNodeHeight)-0.5 {
							t.Fatalf("edge %s -> %s passes through %s at (%.0f, %.0f): %s", edge.From, edge.To, box.Frame.Name, p[0], p[1], edge.Path)
						}
					}
				}
			}
		})
	}
}

// TestEdgeLabelsDoNotOverlap pins the label placement: no two label boxes
// overlap, and each label stays in the gap before its node. In the crossing
// graph, two edges cross at the middle of the gap, where both labels want to
// be. In the dense graph, the 40-unit reach is not enough. In the bipartite
// graphs, a crowded gap puts labels below the columns, and the graph must
// grow to hold them; in bipartite 11 the nearest free place is above the top.
func TestEdgeLabelsDoNotOverlap(t *testing.T) {
	crossing := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion, Name: "crossing",
		Nodes: []schemas.PipelineNode{{Name: "a", Type: "prompt"}, {Name: "b", Type: "prompt"}, {Name: "c", Type: "prompt"}, {Name: "d", Type: "prompt"}},
		Edges: []schemas.PipelineEdge{{From: "a", To: "c"}, {From: "a", To: "d"}, {From: "b", To: "c"}, {From: "b", To: "d"}},
	}
	// dense: two layers of five, each node of the second depends on every node
	// of the first, so 25 labels share one gap.
	dense := &schemas.PipelineTopology{Version: schemas.TopologySchemaVersion, Name: "dense"}
	for i := range 5 {
		dense.Nodes = append(dense.Nodes, schemas.PipelineNode{Name: fmt.Sprintf("a%d", i), Type: "prompt"})
	}
	for i := range 5 {
		to := fmt.Sprintf("b%d", i)
		dense.Nodes = append(dense.Nodes, schemas.PipelineNode{Name: to, Type: "prompt"})
		for j := range 5 {
			dense.Edges = append(dense.Edges, schemas.PipelineEdge{From: fmt.Sprintf("a%d", j), To: to})
		}
	}
	for name, topology := range map[string]*schemas.PipelineTopology{"worst case": pipelineWorstCase(), "crossing": crossing, "dense": dense, "complete 20": completeDAG(20), "bipartite 7": bipartite(7), "bipartite 8": bipartite(8), "bipartite 11": bipartite(11)} {
		t.Run(name, func(t *testing.T) {
			graph, err := presentrun.BuildGraph(topology, presentation.State{})
			if err != nil {
				t.Fatal(err)
			}
			layout := layoutGraph(graph)
			at := map[string]layoutNode{}
			for _, n := range layout.Nodes {
				at[n.Frame.Name] = n
			}
			for i, a := range layout.Edges {
				if a.Pill == "" {
					t.Fatalf("edge %s -> %s has no label", a.From, a.To)
				}
				if x0, y0, x1, y1 := labelBox(a); x0 < 0 || y0 < 0 || x1 > float64(layout.Width) || y1 > float64(layout.Height) {
					t.Errorf("label of %s -> %s at (%d, %d) leaves the %d by %d graph", a.From, a.To, a.MidX, a.MidY, layout.Width, layout.Height)
				}
				if gap := at[a.To].X - layoutGapX/2; a.MidX != gap {
					t.Errorf("label of %s -> %s at x %d, want the middle of the gap before its node, %d", a.From, a.To, a.MidX, gap)
				}
				for _, b := range layout.Edges[i+1:] {
					ax0, ay0, ax1, ay1 := labelBox(a)
					bx0, by0, bx1, by1 := labelBox(b)
					if ax0 < bx1 && bx0 < ax1 && ay0 < by1 && by0 < ay1 {
						t.Errorf("labels of %s -> %s (%d, %d) and %s -> %s (%d, %d) overlap", a.From, a.To, a.MidX, a.MidY, b.From, b.To, b.MidX, b.MidY)
					}
				}
			}
		})
	}
}

// TestCutKeepsTextInColumns pins the cut rules that the scripts copy: a cut
// counts text columns, a wide rune is two, a combining mark is none and stays
// with its base, and a name keeps its start and its end.
func TestCutKeepsTextInColumns(t *testing.T) {
	for _, tc := range []struct {
		name, in   string
		n          int
		end, mid   string
		endColumns int
	}{
		{"ascii fits", "lint", 21, "lint", "lint", 4},
		{"one character", "t", 21, "t", "t", 1},
		{"exactly at the cap", strings.Repeat("a", 21), 21, strings.Repeat("a", 21), strings.Repeat("a", 21), 21},
		{"one over the cap", strings.Repeat("a", 21) + "b", 21, strings.Repeat("a", 20) + "…", strings.Repeat("a", 10) + "…" + strings.Repeat("a", 9) + "b", 21},
		{"names that differ at the end", "integration_contract_verification_for_billinh", 21, "integration_contract…", "integratio…or_billinh", 21},
		{"wide runes", "コードレビュー担当ノード", 10, "コードレ…", "コー…ード", 9},
		{"combining marks", "e\u0301e\u0301e\u0301e\u0301e\u0301", 4, "e\u0301e\u0301e\u0301…", "e\u0301e\u0301…e\u0301", 4},
		{"a mark whose base is cut", "abcde\u0301", 2, "a…", "a…", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cutEnd(tc.in, tc.n); got != tc.end || textColumns(got) != tc.endColumns {
				t.Errorf("cutEnd(%q, %d) = %q (%d columns), want %q (%d columns)", tc.in, tc.n, got, textColumns(got), tc.end, tc.endColumns)
			}
			if got := cutMiddle(tc.in, tc.n); got != tc.mid || textColumns(got) > tc.n {
				t.Errorf("cutMiddle(%q, %d) = %q (%d columns), want %q", tc.in, tc.n, got, textColumns(got), tc.mid)
			}
		})
	}
	if labelWords("[ ] not started") != "not started" || labelWords("every tier") != "every tier" {
		t.Error("labelWords keeps the marker, or changes a label without one")
	}
}

// TestLayoutSurvivesDegenerateGraphs pins that the layout and Auto-layout
// return a deterministic result for graphs that a draft or a run can hold:
// no nodes, one node, a run plan with no edges, a dependency that is not in
// the graph, duplicate names, a cycle, a tall column, and a long chain.
func TestLayoutSurvivesDegenerateGraphs(t *testing.T) {
	named := func(n int, edge func(i int) []int) *schemas.PipelineTopology {
		top := &schemas.PipelineTopology{Version: schemas.TopologySchemaVersion, Name: "shape"}
		for i := 0; i < n; i++ {
			top.Nodes = append(top.Nodes, schemas.PipelineNode{Name: fmt.Sprintf("n%03d", i), Type: "prompt"})
			for _, j := range edge(i) {
				top.Edges = append(top.Edges, schemas.PipelineEdge{From: fmt.Sprintf("n%03d", j), To: fmt.Sprintf("n%03d", i)})
			}
		}
		return top
	}
	none := func(int) []int { return nil }
	graphs := map[string]presentrun.Graph{
		"no nodes":           {},
		"missing dependency": {Nodes: []presentrun.GraphNode{{Name: "a", Depth: 1, Dependencies: []string{"ghost"}}}},
	}
	for name, top := range map[string]*schemas.PipelineTopology{
		"one node":        named(1, none),
		"tall column":     named(200, none),
		"chain of 60":     named(60, func(i int) []int { return map[bool][]int{true: {i - 1}}[i > 0] }),
		"duplicate names": {Version: schemas.TopologySchemaVersion, Name: "dup", Nodes: []schemas.PipelineNode{{Name: "a"}, {Name: "a"}, {Name: "b"}}, Edges: []schemas.PipelineEdge{{From: "a", To: "b"}}},
	} {
		graph, err := presentrun.BuildGraph(top, presentation.State{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		graphs[name] = graph
	}
	plan, err := presentrun.BuildGraph(nil, presentation.State{Plan: presentation.Plan{Stages: []string{"code_writer", "static_analyzer", "test_runner"}}})
	if err != nil {
		t.Fatal(err)
	}
	graphs["run plan with no edges"] = plan
	for name, graph := range graphs {
		first := renderGraph(t, layoutGraph(graph))
		if again := renderGraph(t, layoutGraph(graph)); again != first {
			t.Errorf("%s: two layouts of one graph differ", name)
		}
		for _, edge := range layoutGraph(graph).Edges {
			if edge.Pill != "" && name == "run plan with no edges" {
				t.Errorf("%s: edge %s -> %s has a label without a known payload", name, edge.From, edge.To)
			}
		}
	}
	// Auto-layout of a document that is not a valid graph: a cycle and a
	// dependency on a missing node get the fallback row and no routed paths.
	for name, doc := range map[string]string{
		"cycle":       `{"nodes":[{"name":"a"},{"name":"b"}],"edges":[{"from":"a","to":"b"},{"from":"b","to":"a"}]}`,
		"self edge":   `{"nodes":[{"name":"a"}],"edges":[{"from":"a","to":"a"}]}`,
		"missing end": `{"nodes":[{"name":"a"}],"edges":[{"from":"ghost","to":"a"}]}`,
		"no nodes":    `{"nodes":[],"edges":[{"from":"a","to":"b"}]}`,
	} {
		positions, routes, err := layoutDoc([]byte(doc))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var loose schemas.PipelineTopology
		if err := json.Unmarshal([]byte(doc), &loose); err != nil {
			t.Fatal(err)
		}
		if len(positions) != len(loose.Nodes) || len(routes) != 0 {
			t.Errorf("%s: %d positions and %d routes, want %d positions and none", name, len(positions), len(routes), len(loose.Nodes))
		}
	}
}

// TestLayoutKeyFollowsTheEdges pins the layout key: an edge payload, a new
// edge, and a renamed node change it; a status change does not.
func TestLayoutKeyFollowsTheEdges(t *testing.T) {
	key := func(edit func(*schemas.PipelineTopology)) string {
		t.Helper()
		top := pipelineDiamond()
		edit(top)
		graph, err := presentrun.BuildGraph(top, presentation.State{})
		if err != nil {
			t.Fatal(err)
		}
		return layoutGraph(graph).Key
	}
	base := key(func(*schemas.PipelineTopology) {})
	for name, edit := range map[string]func(*schemas.PipelineTopology){
		"payload": func(top *schemas.PipelineTopology) { top.Edges[2].Payload = schemas.EdgePayloadSummary },
		"edge": func(top *schemas.PipelineTopology) {
			top.Edges = append(top.Edges, schemas.PipelineEdge{From: "code_writer", To: "test_runner"})
		},
		"rename": func(top *schemas.PipelineTopology) {
			top.Nodes[1].Name = "vet"
			top.Edges[0].To, top.Edges[2].From = "vet", "vet"
		},
	} {
		if key(edit) == base {
			t.Errorf("a changed %s kept the layout key", name)
		}
	}
	if key(func(top *schemas.PipelineTopology) { top.Description = "other" }) != base {
		t.Error("a change that is not drawn changed the layout key")
	}
}

// completeDAG has n command nodes, and each node depends on every node
// before it: the most edges, and the longest edges, that n nodes can have.
func completeDAG(n int) *schemas.PipelineTopology {
	top := &schemas.PipelineTopology{Version: schemas.TopologySchemaVersion, Name: "complete"}
	for i := range n {
		name := fmt.Sprintf("n%02d", i)
		top.Nodes = append(top.Nodes, schemas.PipelineNode{Name: name, Type: schemas.NodeTypeCommand, Command: []string{"true"}})
		for j := range i {
			top.Edges = append(top.Edges, schemas.PipelineEdge{From: fmt.Sprintf("n%02d", j), To: name})
		}
	}
	return top
}

// TestLayoutOfALargeGraphIsFast bounds the layout work: the viewer lays the
// graph out under its lock on each run snapshot. On the machine that set the
// bounds, the complete graph of 40 nodes took 7 ms (43 ms with -race), of 60
// nodes 25 ms (155 ms with -race), and the bipartite graph of 40 by 40 (1,600
// labels in one gap) 26 ms (165 ms with -race); each bound is more than ten
// times that. Under the race detector, which slows code by a factor of 2 to
// 20 depending on the machine, the bounds scale by ten. The layout still runs
// and the edge count is still checked.
func TestLayoutOfALargeGraphIsFast(t *testing.T) {
	scale := time.Duration(1)
	if raceEnabled {
		scale = 10
	}
	for _, tc := range []struct {
		name     string
		topology *schemas.PipelineTopology
		edges    int
		bound    time.Duration
	}{
		{"complete 40", completeDAG(40), 40 * 39 / 2, scale * time.Second},
		{"complete 60", completeDAG(60), 60 * 59 / 2, scale * 2 * time.Second},
		{"bipartite 40", bipartite(40), 40 * 40, scale * 2 * time.Second},
	} {
		graph, err := presentrun.BuildGraph(tc.topology, presentation.State{})
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		layout := layoutGraph(graph)
		if took := time.Since(start); took > tc.bound {
			t.Errorf("the layout of %s took %v, want under %v", tc.name, took, tc.bound)
		}
		if len(layout.Edges) != tc.edges {
			t.Fatalf("%s: %d edges, want %d", tc.name, len(layout.Edges), tc.edges)
		}
	}
}

// bipartite is two layers of k nodes, and each node of the second layer
// depends on every node of the first: k*k labels in one gap.
func bipartite(k int) *schemas.PipelineTopology {
	top := &schemas.PipelineTopology{Version: schemas.TopologySchemaVersion, Name: "bipartite"}
	for i := range 2 * k {
		top.Nodes = append(top.Nodes, schemas.PipelineNode{Name: fmt.Sprintf("n%03d", i), Type: "prompt"})
	}
	for j := k; j < 2*k; j++ {
		for i := range k {
			top.Edges = append(top.Edges, schemas.PipelineEdge{From: fmt.Sprintf("n%03d", i), To: fmt.Sprintf("n%03d", j)})
		}
	}
	return top
}

// TestLayoutBytesRepeat pins that the unstable column sort, with the place as
// tie-break, gives the same bytes on every run, for a graph with skipped
// columns, a dense graph, and a dense gap.
func TestLayoutBytesRepeat(t *testing.T) {
	for name, top := range map[string]*schemas.PipelineTopology{"worst case": pipelineWorstCase(), "complete 20": completeDAG(20), "bipartite 5x5": bipartite(5), "bipartite 11": bipartite(11)} {
		graph, err := presentrun.BuildGraph(top, presentation.State{})
		if err != nil {
			t.Fatal(err)
		}
		want := renderGraph(t, layoutGraph(graph))
		for range 50 {
			if renderGraph(t, layoutGraph(graph)) != want {
				t.Fatalf("%s: a render differs from the first", name)
			}
		}
	}
}
