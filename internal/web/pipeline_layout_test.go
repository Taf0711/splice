package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// pipelineDiamond is one writer, two parallel checks (one a command node), and
// a runner that waits for both.
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
			{From: "lint", To: "test_runner"},
			{From: "static_analyzer", To: "test_runner"},
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

// TestPipelineGraphGolden pins the SVG of a live diamond run.
func TestPipelineGraphGolden(t *testing.T) {
	graph, err := presentrun.BuildGraph(pipelineDiamond(), pipelineRun(t))
	if err != nil {
		t.Fatal(err)
	}
	got := []byte(renderGraph(t, layoutGraph(graph)) + "\n")
	path := filepath.Join("testdata", "pipeline_graph_diamond.golden")
	if os.Getenv("SPLICE_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v (create it with SPLICE_UPDATE_GOLDEN=1)", err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("golden mismatch (regenerate with SPLICE_UPDATE_GOLDEN=1):\n--- want\n%s--- got\n%s", want, got)
	}
}

// TestPipelineLayoutIsDeterministic pins the layout rules: the same graph
// gives the same bytes, a depth is one row, a row keeps the graph order, and
// the key follows the structure, not the status.
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
	if lint.Y != analyzer.Y || lint.X >= analyzer.X {
		t.Fatalf("depth-1 row: lint at (%d,%d), static_analyzer at (%d,%d); want one row in declaration order", lint.X, lint.Y, analyzer.X, analyzer.Y)
	}
	if !(at["code_writer"].Y < lint.Y && lint.Y < at["test_runner"].Y) {
		t.Fatal("rows must run top to bottom by depth")
	}
	if len(layout.Edges) != 4 {
		t.Fatalf("edges = %d, want 4", len(layout.Edges))
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
	if !strings.Contains(svg, `<text class="n-status" x="14" y="60">`+cutRunes(full, layoutLineRunes)+`</text>`) || !strings.Contains(cutRunes(full, layoutLineRunes), "…") {
		t.Fatalf("the status line is not cut in the box:\n%s", svg)
	}
	if !strings.Contains(svg, full+"</title>") {
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
		fmt.Sprintf("const NW = %d, NH = %d, PAD = %d, GAP = %d;", layoutNodeWidth, layoutNodeHeight, layoutPad, layoutGapY),
		fmt.Sprintf("cut(name, %d)", layoutNameRunes),
		fmt.Sprintf("cut(subtitle(n), %d)", layoutLineRunes),
		fmt.Sprintf("cut(label(n, probs), %d)", layoutLineRunes),
	} {
		if !strings.Contains(editor, want) {
			t.Errorf("editor.js lacks %q", want)
		}
	}
	if want := fmt.Sprintf("r.length > %d ? r.slice(0, %d)", layoutLineRunes, layoutLineRunes-1); !strings.Contains(viewer, want) {
		t.Errorf("pipeline.js lacks %q", want)
	}
}
