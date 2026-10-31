package presentrun_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// liveState compiles the topology at the tier and applies the plan event the
// runtime emits, then the given stage events, through the real reducer.
func liveState(t *testing.T, topology *schemas.PipelineTopology, tier schemas.PipelineTier, events ...presentation.StreamEventLike) presentation.State {
	t.Helper()
	compiled, err := splicerun.CompileTopology(topology, tier)
	if err != nil {
		t.Fatalf("compile at %s: %v", tier, err)
	}
	plan := schemas.ExecutionPlan{RequestIntent: "x", Tier: tier, Stages: compiled.Stages, TopologyName: topology.Name}
	planEvent := agent.PipelinePlanEvent{Dependencies: map[string][]string{}}
	for _, stage := range compiled.Stages {
		planEvent.Stages = append(planEvent.Stages, stage.Name)
		if len(stage.DependsOn) > 0 {
			planEvent.Dependencies[stage.Name] = stage.DependsOn
		}
	}
	state, err := presentation.Apply(presentation.State{}, presentrun.AdaptPipelinePlan(plan, planEvent))
	if err != nil {
		t.Fatalf("apply plan: %v", err)
	}
	for _, event := range events {
		state, err = presentation.Apply(state, event)
		if err != nil {
			t.Fatalf("apply %T: %v", event, err)
		}
	}
	return state
}

func stage(name string, iteration int, status, detail string) presentation.StreamEventLike {
	return presentrun.AdaptStageEvent(agent.StageEvent{Name: name, Iteration: iteration, Status: status, Detail: detail})
}

type row struct {
	name    string
	depth   int
	deps    string
	skipped string
}

func rows(graph presentrun.Graph) []row {
	out := make([]row, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		out = append(out, row{node.Name, node.Depth, strings.Join(node.Dependencies, ","), strings.Join(node.SkippedAt, ",")})
	}
	return out
}

func diamondTopology() *schemas.PipelineTopology {
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

func TestBuildGraphDefaultTopologyAtEachTier(t *testing.T) {
	topology := splicerun.DefaultTopology()
	all := []row{
		{"code_writer", 0, "", ""},
		{"test_generator", 1, "code_writer", ""},
		{"static_analyzer", 1, "code_writer", ""},
		{"security_auditor", 2, "test_generator,code_writer,static_analyzer", ""},
		{"test_runner", 2, "static_analyzer", ""},
		{"acceptance_verifier", 3, "test_runner", ""},
	}
	skippedAt := map[schemas.PipelineTier][]string{
		schemas.TierTrivial:       {"test_generator", "static_analyzer", "security_auditor", "test_runner", "acceptance_verifier"},
		schemas.TierLight:         {"test_generator", "security_auditor"},
		schemas.TierStandard:      {"security_auditor"},
		schemas.TierSubstantial:   nil,
		schemas.TierArchitectural: nil,
	}
	for tier, skipped := range skippedAt {
		t.Run(string(tier), func(t *testing.T) {
			graph, err := presentrun.BuildGraph(topology, liveState(t, topology, tier))
			if err != nil {
				t.Fatal(err)
			}
			if !graph.Live || graph.Tier != string(tier) || graph.Topology != "default" || len(graph.Notes) != 0 {
				t.Fatalf("graph header = live %v tier %q topology %q notes %v", graph.Live, graph.Tier, graph.Topology, graph.Notes)
			}
			want := append([]row(nil), all...)
			for i := range want {
				for _, name := range skipped {
					if want[i].name == name {
						want[i].skipped = string(tier)
					}
				}
			}
			if got := rows(graph); !reflect.DeepEqual(got, want) {
				t.Fatalf("rows =\n%v\nwant\n%v", got, want)
			}
		})
	}
}

func TestBuildGraphStaticViewListsSkippedTiers(t *testing.T) {
	graph, err := presentrun.BuildGraph(splicerun.DefaultTopology(), presentation.State{})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Live || graph.Tier != "" {
		t.Fatalf("static graph live=%v tier=%q", graph.Live, graph.Tier)
	}
	got := map[string]string{}
	for _, node := range graph.Nodes {
		got[node.Name] = strings.Join(node.SkippedAt, ",")
		if node.Status != "" {
			t.Fatalf("static node %s has status %q", node.Name, node.Status)
		}
	}
	want := map[string]string{
		"code_writer":         "",
		"test_generator":      "trivial,light",
		"static_analyzer":     "trivial",
		"security_auditor":    "trivial,light,standard",
		"test_runner":         "trivial",
		"acceptance_verifier": "trivial",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("skipped tiers = %v, want %v", got, want)
	}
}

func TestBuildGraphDiamond(t *testing.T) {
	topology := diamondTopology()
	state := liveState(t, topology, schemas.TierStandard,
		stage("code_writer", 0, "running", ""),
		stage("code_writer", 0, "completed", "wrote hello.go"),
		stage("lint", 0, "running", ""),
		stage("lint", 0, "failed", `command node lint: command "go vet ./..." failed: exit 1`),
	)
	graph, err := presentrun.BuildGraph(topology, state)
	if err != nil {
		t.Fatal(err)
	}
	want := []row{
		{"code_writer", 0, "", ""},
		{"lint", 1, "code_writer", ""},
		{"static_analyzer", 1, "code_writer", ""},
		{"test_runner", 2, "lint,static_analyzer", ""},
	}
	if got := rows(graph); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	lint := graph.Nodes[1]
	if lint.Status != presentation.NodeStatusFailed || lint.Command != "go vet ./..." || !lint.ModelFree || !strings.Contains(lint.Summary, "failed") {
		t.Fatalf("lint node = %+v", lint)
	}
	if graph.Nodes[2].Status != "" || graph.Nodes[0].Summary != "wrote hello.go" {
		t.Fatalf("not-started node has a status or the summary is lost: %+v", graph.Nodes)
	}
}

func TestBuildGraphDisconnectedNode(t *testing.T) {
	topology := diamondTopology()
	topology.Nodes = append(topology.Nodes, schemas.PipelineNode{Name: "notes", Type: schemas.NodeTypePrompt, Prompt: "summarize"})
	graph, err := presentrun.BuildGraph(topology, presentation.State{})
	if err != nil {
		t.Fatal(err)
	}
	// A node with no edge sits at depth 0, after the declared depth-0 nodes.
	if got := rows(graph); got[0].name != "code_writer" || got[1].name != "notes" || got[1].depth != 0 || got[1].deps != "" {
		t.Fatalf("rows = %v, want notes at depth 0 after code_writer", got)
	}
}

func TestBuildGraphReenteredIterationShowsLatestPass(t *testing.T) {
	topology := splicerun.DefaultTopology()
	cost := 0.25
	state := liveState(t, topology, schemas.TierLight,
		stage("code_writer", 0, "running", ""),
		stage("code_writer", 0, "completed", "first pass"),
		presentation.UsageEvent{NodeID: "code_writer", Iteration: 0, Usage: presentation.TokenUsage{InputTokens: 100, OutputTokens: 10}, CostUSD: &cost},
		stage("code_writer", 1, "running", ""),
		presentation.UsageEvent{NodeID: "code_writer", Iteration: 1, Usage: presentation.TokenUsage{InputTokens: 40, OutputTokens: 4}},
	)
	graph, err := presentrun.BuildGraph(topology, state)
	if err != nil {
		t.Fatal(err)
	}
	writer := graph.Nodes[0]
	if writer.Name != "code_writer" || writer.Iteration != 1 || writer.Passes != 2 || writer.Status != presentation.NodeStatusRunning {
		t.Fatalf("writer = %+v, want pass 2 running", writer)
	}
	// Tokens and cost sum both passes; the unpriced request of pass 2 makes
	// the cost a lower bound. Input and output stay the latest pass, which
	// the applied budget belongs to.
	if writer.Tokens != 154 || writer.CostUSD != 0.25 || !writer.CostUnpriced || writer.InputTokens != 40 || writer.OutputTokens != 4 {
		t.Fatalf("writer spend = %+v", writer)
	}
	// The last summary is the pass-0 summary until the new pass ends, and it
	// says which pass reported it.
	if writer.Summary != "first pass" || writer.SummaryIteration != 0 {
		t.Fatalf("summary = %q from pass %d, want the pass-1 terminal summary", writer.Summary, writer.SummaryIteration+1)
	}
}

func TestBuildGraphSkippedAtTierNode(t *testing.T) {
	topology := splicerun.DefaultTopology()
	graph, err := presentrun.BuildGraph(topology, liveState(t, topology, schemas.TierLight))
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Nodes {
		if node.Name == "security_auditor" {
			if !reflect.DeepEqual(node.SkippedAt, []string{"light"}) || node.Status != "" {
				t.Fatalf("security_auditor = %+v, want skipped at light", node)
			}
			return
		}
	}
	t.Fatal("security_auditor is missing from the graph")
}

func TestBuildGraphDropsTopologyThatChangedAfterRunStart(t *testing.T) {
	state := liveState(t, splicerun.DefaultTopology(), schemas.TierLight)
	graph, err := presentrun.BuildGraph(diamondTopology(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Notes) != 1 || graph.Topology != "default" {
		t.Fatalf("notes = %v topology = %q, want the changed-topology notice", graph.Notes, graph.Topology)
	}
	// The plan is the truth: only the planned nodes, with their run deps.
	want := []row{
		{"code_writer", 0, "", ""},
		{"static_analyzer", 1, "code_writer", ""},
		{"test_runner", 2, "static_analyzer", ""},
		{"acceptance_verifier", 3, "test_runner", ""},
	}
	if got := rows(graph); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

func TestBuildGraphFailsLoud(t *testing.T) {
	if _, err := presentrun.BuildGraph(nil, presentation.State{}); err == nil {
		t.Fatal("BuildGraph accepted no topology and no run")
	}
	state := presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Plan:          presentation.Plan{Stages: []string{"a"}, Dependencies: map[string][]string{"a": {"ghost"}}},
	}
	if _, err := presentrun.BuildGraph(nil, state); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("err = %v, want the unknown dependency named", err)
	}
}

// TestUsageReachesNodeThroughReducer pins the live update: a usage event adds
// spend to its (node, iteration), and an unknown node is refused by name.
func TestUsageReachesNodeThroughReducer(t *testing.T) {
	state := liveState(t, splicerun.DefaultTopology(), schemas.TierTrivial, stage("code_writer", 0, "running", ""))
	cost := 0.5
	state, err := presentation.Apply(state, presentrun.AdaptAttributedUsage(agent.AttributedUsage{
		Stage: "code_writer", Iteration: 1,
		Usage: agent.Usage{InputTokens: 7, OutputTokens: 3},
		Cost:  agent.UsageCostEstimate{CostUSD: &cost, Status: agent.CostStatusPriced},
	}))
	if err != nil {
		t.Fatal(err)
	}
	node := state.Nodes[0]
	if node.Cost.Tokens != 10 || node.Cost.USD != 0.5 || node.Cost.Unpriced || node.Usage.InputTokens != 7 {
		t.Fatalf("node spend = %+v %+v", node.Cost, node.Usage)
	}
	if _, err := presentation.Apply(state, presentation.UsageEvent{NodeID: "ghost"}); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("err = %v, want the unknown node named", err)
	}
}

// TestBuildGraphNamesFlagRemoval pins finding 5 of the Phase 1 review: a node
// the flag set removed says why, and is not reported as a tier skip.
func TestBuildGraphNamesFlagRemoval(t *testing.T) {
	topology := splicerun.DefaultTopology()
	compiled, err := splicerun.CompileTopology(topology, schemas.TierSubstantial)
	if err != nil {
		t.Fatal(err)
	}
	plan := schemas.ExecutionPlan{RequestIntent: "x", Tier: schemas.TierSubstantial, TopologyName: "default",
		RemovedStages: map[string]string{"security_auditor": "flag splice.stage.security_auditor is off"}}
	event := agent.PipelinePlanEvent{Dependencies: map[string][]string{}}
	for _, stage := range compiled.Stages {
		if stage.Name == "security_auditor" {
			continue
		}
		plan.Stages = append(plan.Stages, stage)
		event.Stages = append(event.Stages, stage.Name)
		if len(stage.DependsOn) > 0 {
			event.Dependencies[stage.Name] = stage.DependsOn
		}
	}
	state, err := presentation.Apply(presentation.State{}, presentrun.AdaptPipelinePlan(plan, event))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := presentrun.BuildGraph(topology, state)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Nodes {
		if node.Name == "security_auditor" {
			if node.Removed != "flag splice.stage.security_auditor is off" || len(node.SkippedAt) != 0 {
				t.Fatalf("security_auditor = %+v, want the flag removal and no tier skip", node)
			}
			return
		}
	}
	t.Fatal("security_auditor is missing")
}

// TestBuildGraphEndedRunSaysNotRun pins finding 6 of the Phase 1 review: a
// skipped stage keeps its own status, and after the run ends a planned node
// that never started is "not run", not pending.
func TestBuildGraphEndedRunSaysNotRun(t *testing.T) {
	topology := splicerun.DefaultTopology()
	state := liveState(t, topology, schemas.TierLight,
		stage("code_writer", 0, "running", ""),
		stage("code_writer", 0, "completed", "wrote it"),
		stage("static_analyzer", 0, "running", ""),
		stage("static_analyzer", 0, "skipped", "no checker for this language"),
		stage("test_runner", 0, "running", ""),
		stage("test_runner", 0, "failed", "tests failed"),
		presentrun.AdaptRunEvent("failed", "tests failed"),
	)
	graph, err := presentrun.BuildGraph(topology, state)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]presentrun.GraphNode{}
	for _, node := range graph.Nodes {
		byName[node.Name] = node
	}
	if got := byName["static_analyzer"]; got.Status != presentation.NodeStatusSkipped || got.NotRun {
		t.Fatalf("static_analyzer = %+v, want skipped", got)
	}
	if got := byName["acceptance_verifier"]; !got.NotRun || got.Status != "" {
		t.Fatalf("acceptance_verifier = %+v, want not run", got)
	}
	if got := byName["security_auditor"]; got.NotRun || len(got.SkippedAt) == 0 {
		t.Fatalf("security_auditor = %+v, want a tier skip, not \"not run\"", got)
	}
}

// TestAdaptAttributedUsageShiftsTheIteration pins the 1-based to 0-based
// pass shift. Both passes exist, so the latest-pass fallback in the reducer
// cannot hide a missing shift: runtime pass 1 must land on node iteration 0.
func TestAdaptAttributedUsageShiftsTheIteration(t *testing.T) {
	state := liveState(t, splicerun.DefaultTopology(), schemas.TierTrivial,
		stage("code_writer", 0, "running", ""),
		stage("code_writer", 0, "completed", "first"),
		stage("code_writer", 1, "running", ""),
	)
	state, err := presentation.Apply(state, presentrun.AdaptAttributedUsage(agent.AttributedUsage{
		Stage: "code_writer", Iteration: 1, Usage: agent.Usage{InputTokens: 9, OutputTokens: 1},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range state.Nodes {
		want := int64(0)
		if node.Iteration == 0 {
			want = 10
		}
		if node.Cost.Tokens != want {
			t.Fatalf("node %s iteration %d tokens = %d, want %d", node.ID, node.Iteration, node.Cost.Tokens, want)
		}
	}
}

// TestBuildGraphMarksANodeRunningAtTheEnd pins that a node still running when
// the run ended is not shown as running: a cancel stops it.
func TestBuildGraphMarksANodeRunningAtTheEnd(t *testing.T) {
	topology := splicerun.DefaultTopology()
	state := liveState(t, topology, schemas.TierLight,
		stage("code_writer", 0, "running", ""),
		presentrun.AdaptRunEvent("cancelled", ""),
	)
	graph, err := presentrun.BuildGraph(topology, state)
	if err != nil {
		t.Fatal(err)
	}
	writer := graph.Nodes[0]
	if !writer.Interrupted || presentrun.StatusText(writer) != "stopped when the run ended" {
		t.Fatalf("writer = %+v (%q), want it stopped by the run end", writer, presentrun.StatusText(writer))
	}
}
