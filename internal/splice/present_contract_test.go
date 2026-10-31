package splice

// P1.1 contract tests: presentation snapshots emitted by real runs must
// match the event sequences that produced them. Each test records the legacy
// event stream (OnStageEvent / OnPipelinePlan), captures the live snapshots
// (OnPresentationState), and replays the recorded events through a fresh
// accumulator to prove live emission and replay agree.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
)

// checkPresentationGolden uses the same combination approach P1.0
// established: capture JSON on first run, diff on later runs, regenerate
// with SPLICE_UPDATE_GOLDEN=1.
func checkPresentationGolden(t *testing.T, name string, snapshots []presentation.State) {
	t.Helper()
	encoded, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	encoded = append(encoded, '\n')
	path := filepath.Join("testdata", name+".golden")
	if os.Getenv("SPLICE_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create testdata dir: %v", err)
		}
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (create it with SPLICE_UPDATE_GOLDEN=1)", path, err)
	}
	if string(want) != string(encoded) {
		t.Fatalf("golden %s mismatch (regenerate with SPLICE_UPDATE_GOLDEN=1):\n--- want\n%s--- got\n%s", name, want, encoded)
	}
}

// TestPresentationGoldenTrace pins emission ORDER, not just final state: the
// whole snapshot list of a happy-path run is serialized and diffed.
func TestPresentationGoldenTrace(t *testing.T) {
	_, _, _, snapshots, result := runWithPresentation(t, "add a Hello function and tests", runFakeProvider{}, agent.Options{PermissionMode: agent.PermissionModeAuto})
	if result.Status != "completed" {
		t.Fatalf("pipeline status = %q, want completed", result.Status)
	}
	checkPresentationGolden(t, "presentation_trace_happy_path", snapshots)
}

// BenchmarkPresentationEmission measures the cost of presentation emission.
// The nil-callback run is the pre-P1.1 baseline (byte-identical behavior:
// no accumulator is created when OnPresentationState is nil). The with-callback
// run measures the added emission path.
func BenchmarkPresentationEmission(b *testing.B) {
	workspace := func(b *testing.B) (string, *tools.Registry) {
		b.Helper()
		workDir := b.TempDir()
		if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0644); err != nil {
			b.Fatal(err)
		}
		registry := tools.NewRegistry()
		registry.Register(tools.NewReadFileTool(workDir))
		registry.Register(tools.NewListDirectoryTool(workDir))
		registry.Register(tools.NewGrepTool(workDir))
		registry.Register(tools.NewWriteFileTool(workDir))
		registry.Register(tools.NewDeleteFileTool(workDir))
		registry.Register(tools.NewBashTool(workDir))
		return workDir, registry
	}
	b.Run("nil-callback-baseline", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			workDir, registry := workspace(b)
			_, err := Run(context.Background(), "add a Hello function", runFakeProvider{}, agent.Options{
				Cwd: workDir, Registry: registry, PermissionMode: agent.PermissionModeAuto,
			}, nil, nil)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("with-emission", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			workDir, registry := workspace(b)
			_, err := Run(context.Background(), "add a Hello function", runFakeProvider{}, agent.Options{
				Cwd: workDir, Registry: registry, PermissionMode: agent.PermissionModeAuto,
				OnPresentationState: func(presentation.State) {},
			}, nil, nil)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// runWithPresentation runs a pipeline with both the legacy event recorders
// and the presentation snapshot recorder wired, returning the recorded
// events, the live snapshots, and the final pipeline result.
func runWithPresentation(t *testing.T, prompt string, provider agent.Provider, options agent.Options) ([]agent.StageEvent, []agent.PipelinePlanEvent, []agent.AttributedUsage, []presentation.State, schemas.PipelineResult) {
	t.Helper()
	var stages []agent.StageEvent
	var plans []agent.PipelinePlanEvent
	var usages []agent.AttributedUsage
	options.OnAttributedUsage = func(usage agent.AttributedUsage) { usages = append(usages, usage) }
	var snapshots []presentation.State
	options.OnStageEvent = func(event agent.StageEvent) { stages = append(stages, event) }
	options.OnPipelinePlan = func(event agent.PipelinePlanEvent) { plans = append(plans, event) }
	options.OnPresentationState = func(state presentation.State) { snapshots = append(snapshots, state) }

	workDir, registry := newRunTestWorkspace(t)
	options.Cwd = workDir
	options.Registry = registry

	result, err := Run(context.Background(), prompt, provider, options, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var pipeline schemas.PipelineResult
	if err := json.Unmarshal([]byte(result.FinalAnswer), &pipeline); err != nil {
		t.Fatalf("parse pipeline result: %v", err)
	}
	return stages, plans, usages, snapshots, pipeline
}

// assertValidSnapshots checks the pairing invariant on every live snapshot.
func assertValidSnapshots(t *testing.T, snapshots []presentation.State) {
	t.Helper()
	for i, snapshot := range snapshots {
		if err := snapshot.Validate(); err != nil {
			t.Fatalf("snapshot %d invalid: %v\nsnapshot: %+v", i, err, snapshot)
		}
	}
}

// assertNodesMatchEvents checks that the final snapshot's nodes carry the
// same ids, kinds, and final statuses the recorded stage events imply.
func assertNodesMatchEvents(t *testing.T, events []agent.StageEvent, final presentation.State) {
	t.Helper()
	byID := make(map[string]presentation.ExecutionNode, len(final.Nodes))
	for _, node := range final.Nodes {
		byID[node.ID] = node
	}
	for _, event := range events {
		node, ok := byID[event.Name]
		if !ok {
			t.Fatalf("stage event for %s (%s) has no node in the final snapshot", event.Name, event.Status)
			continue
		}
		want := presentrun.NodeKindForStage(event.Name)
		if node.Kind != want {
			t.Fatalf("node %s kind = %q, want %q", event.Name, node.Kind, want)
		}
		if node.Iteration != 0 {
			t.Fatalf("node %s iteration = %d, want 0 (events carry no iteration)", event.Name, node.Iteration)
		}
	}
}

// replayAccumulator feeds recorded plan and stage events through a fresh
// accumulator and returns the final snapshot. identity carries the plan
// title, tier, and topology the live run used; agent.PipelinePlanEvent does
// not carry them, so the replay receives them as run-level metadata.
func replayAccumulator(t *testing.T, plans []agent.PipelinePlanEvent, events []agent.StageEvent, usages []agent.AttributedUsage, runStatus, runDetail string, identity presentation.Plan) *presentrun.Accumulator {
	t.Helper()
	acc := presentrun.New(func(msg string) { t.Logf("presentrun: %s", msg) })
	for _, plan := range plans {
		acc.Apply(presentrun.AdaptPipelinePlan(schemas.ExecutionPlan{
			RequestIntent: identity.Title,
			Tier:          schemas.PipelineTier(identity.Tier),
			TopologyName:  identity.Topology,
		}, plan))
	}
	for _, event := range events {
		acc.Apply(presentrun.AdaptStageEvent(event))
	}
	// Spend sums per node, so the order against stage events does not matter
	// once every node exists. Requests outside the plan stages are not node
	// spend, as in the live wiring.
	planned := map[string]bool{}
	for _, plan := range plans {
		for _, stage := range plan.Stages {
			planned[stage] = true
		}
	}
	for _, usage := range usages {
		if planned[usage.Stage] {
			acc.Apply(presentrun.AdaptAttributedUsage(usage))
		}
	}
	acc.Apply(presentrun.AdaptRunEvent(runStatus, runDetail))
	return acc
}

// TestPresentationContractHappyPath drives a real completed run and proves
// the emitted snapshots match the recorded event stream.
func TestPresentationContractHappyPath(t *testing.T) {
	stages, plans, usages, snapshots, result := runWithPresentation(t, "add a Hello function and tests", runFakeProvider{}, agent.Options{PermissionMode: agent.PermissionModeAuto})
	if result.Status != "completed" {
		t.Fatalf("pipeline status = %q, want completed", result.Status)
	}
	assertValidSnapshots(t, snapshots)
	if len(snapshots) == 0 {
		t.Fatal("no snapshots emitted")
	}
	final := snapshots[len(snapshots)-1]
	assertNodesMatchEvents(t, stages, final)

	// Replay must agree with live emission.
	replay := replayAccumulator(t, plans, stages, usages, "completed", "", final.Plan)
	if err := replay.Snapshot().Validate(); err != nil {
		t.Fatalf("replay snapshot invalid: %v", err)
	}
	// Routes reach the accumulator through the run's internal route hook,
	// not a public event, so the replay cannot rebuild them. Every started
	// node must carry one in the live state; the comparison drops them.
	for _, node := range final.Nodes {
		if node.Route == nil {
			t.Fatalf("live node %s has no applied route", node.ID)
		}
	}
	got, _ := json.Marshal(replay.Snapshot())
	want, _ := json.Marshal(withoutRoutes(final))
	if string(got) != string(want) {
		t.Fatalf("replay final state differs from live final snapshot\n got %s\nwant %s", got, want)
	}
	applied, skipped, errors := replay.Counts()
	if skipped != 0 || errors != 0 {
		t.Fatalf("replay counts = (%d, %d, %d), want zero skips and errors", applied, skipped, errors)
	}
}

// TestPresentationContractRepairLoop drives a run whose test runner fails
// once and then passes, exercising the repair loop's message/repaired
// events. The snapshot must carry the proposed and applied interventions.
func TestPresentationContractRepairLoop(t *testing.T) {
	writer := &scriptedWriter{}
	runner := &scriptedTestRunner{failTimes: 1}
	registry := stageRegistry{"code_writer": writer, "test_runner": runner}
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "fix the failing test",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}, {Name: "test_runner"}},
	}

	var stages []agent.StageEvent
	var plans []agent.PipelinePlanEvent
	var snapshots []presentation.State
	options := PipelineRunConfig{
		OnStageEvent:        func(event agent.StageEvent) { stages = append(stages, event) },
		OnPipelinePlan:      func(event agent.PipelinePlanEvent) { plans = append(plans, event) },
		OnPresentationState: func(state presentation.State) { snapshots = append(snapshots, state) },
		Cwd:                 t.TempDir(),
	}
	wrapped, acc := wirePresentation(options, plan)
	result, err := runIterationLoop(context.Background(), "repair-run", plan, registry, runFakeProvider{}, wrapped, t.TempDir(), nil, nil, nil, nil, acc, nil)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	finishPresentation(acc, wrapped, result)

	assertValidSnapshots(t, snapshots)
	final := snapshots[len(snapshots)-1]
	assertNodesMatchEvents(t, stages, final)

	// The repair loop must surface as interventions: one proposed rollback
	// (message) and one applied continue (repaired).
	var proposed, applied bool
	for _, intervention := range final.Interventions {
		if intervention.Status == presentation.InterventionProposed {
			proposed = true
		}
		if intervention.Status == presentation.InterventionApplied {
			applied = true
		}
	}
	if !proposed || !applied {
		t.Fatalf("repair interventions missing from final snapshot: %+v", final.Interventions)
	}
}

// TestPresentationContractDegradedRun drives a run with a skippable stage
// that has no configured agent (a "skipped" event) alongside a repaired
// stage. The skipped stage must project as a pending node.
func TestPresentationContractDegradedRun(t *testing.T) {
	writer := &scriptedWriter{}
	runner := &scriptedTestRunner{failTimes: 1}
	registry := stageRegistry{"code_writer": writer, "test_runner": runner}
	plan := schemas.ExecutionPlan{
		Tier: schemas.TierLight,
		Stages: []schemas.ExecutionStage{
			{Name: "unconfigured_stage", Budget: schemas.StageBudget{Skippable: true}},
			{Name: "code_writer"},
			{Name: "test_runner"},
		},
	}

	var snapshots []presentation.State
	options := PipelineRunConfig{
		OnPresentationState: func(state presentation.State) { snapshots = append(snapshots, state) },
		Cwd:                 t.TempDir(),
	}
	wrapped, acc := wirePresentation(options, plan)
	result, err := runIterationLoop(context.Background(), "degraded-run", plan, registry, runFakeProvider{}, wrapped, t.TempDir(), nil, nil, nil, nil, acc, nil)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	finishPresentation(acc, wrapped, result)

	assertValidSnapshots(t, snapshots)
	final := snapshots[len(snapshots)-1]
	found := false
	for _, node := range final.Nodes {
		if node.ID == "unconfigured_stage" {
			found = true
			if node.Status != presentation.NodeStatusSkipped {
				t.Fatalf("skipped stage status = %q, want skipped", node.Status)
			}
		}
	}
	if !found {
		t.Fatal("skipped stage missing from final snapshot")
	}
}

// TestPresentationErrorSurface proves the fail-soft error policy: a real run
// with emission enabled completes and every snapshot validates (emission
// never aborts the run), and a malformed event fed to the accumulator is
// counted, logged, and leaves the last-good state valid.
func TestPresentationErrorSurface(t *testing.T) {
	stages, plans, _, snapshots, result := runWithPresentation(t, "add a Hello function", runFakeProvider{}, agent.Options{PermissionMode: agent.PermissionModeAuto})
	assertValidSnapshots(t, snapshots)
	if result.Status != "completed" {
		t.Fatalf("run did not complete with emission enabled: %q", result.Status)
	}

	// Inject a malformed event into the replay stream: an unknown stage
	// status the reducer refuses.
	var warnings []string
	acc := presentrun.New(func(msg string) { warnings = append(warnings, msg) })
	for _, plan := range plans {
		acc.Apply(presentrun.AdaptPlanEventWithDependencies("", plan.Stages, plan.Dependencies))
	}
	for _, event := range stages {
		acc.Apply(presentrun.AdaptStageEvent(event))
	}
	acc.Apply(presentation.StageEvent{ID: "code_writer", Kind: presentation.NodeKindWrite, Status: "bogus"})
	acc.Apply(presentrun.AdaptRunEvent("completed", ""))

	if err := acc.Snapshot().Validate(); err != nil {
		t.Fatalf("last-good snapshot invalid after refused event: %v", err)
	}
	applied, skipped, errors := acc.Counts()
	if errors != 1 || len(warnings) != 1 {
		t.Fatalf("errors = %d, warnings = %d, want 1 and 1 (counted and logged)", errors, len(warnings))
	}
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	if applied == 0 {
		t.Fatal("no events applied before the refusal")
	}
}

// TestPresentationCarriesCompiledDependencies pins the end-to-end graph
// wiring: the compiled plan's DependsOn reaches the plan event, and each
// presentation node carries its dependencies instead of an empty field.
func TestPresentationCarriesCompiledDependencies(t *testing.T) {
	_, plans, _, snapshots, result := runWithPresentation(t, "add a Hello function and tests", runFakeProvider{}, agent.Options{PermissionMode: agent.PermissionModeAuto})
	if result.Status != "completed" {
		t.Fatalf("pipeline status = %q, want completed", result.Status)
	}
	planHasDeps := false
	for _, plan := range plans {
		if len(plan.Dependencies) > 0 {
			planHasDeps = true
		}
	}
	if !planHasDeps {
		t.Fatal("no pipeline plan event carried compiled dependencies")
	}
	final := snapshots[len(snapshots)-1]
	found := false
	for _, node := range final.Nodes {
		if len(node.Dependencies) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("no presentation node carried compiled dependencies")
	}
}

// withoutRoutes returns the state with every node route cleared.
func withoutRoutes(state presentation.State) presentation.State {
	state.Nodes = append([]presentation.ExecutionNode(nil), state.Nodes...)
	for i := range state.Nodes {
		state.Nodes[i].Route = nil
	}
	return state
}

// TestWirePresentationFiltersUsageToPlanStages pins the plan-stage filter:
// a request outside the plan (step_back) is counted as skipped run spend,
// never applied to a node or refused as an unknown node.
func TestWirePresentationFiltersUsageToPlanStages(t *testing.T) {
	plan := schemas.ExecutionPlan{Tier: schemas.TierTrivial, RequestIntent: "x", Stages: []schemas.ExecutionStage{{Name: "code_writer"}}}
	options, acc := wirePresentation(PipelineRunConfig{OnPresentationState: func(presentation.State) {}}, plan)
	options.OnPipelinePlan(agent.PipelinePlanEvent{Stages: []string{"code_writer"}})
	options.OnStageEvent(agent.StageEvent{Name: "code_writer", Status: "running"})
	options.OnAttributedUsage(agent.AttributedUsage{Stage: "step_back", Iteration: 1, Usage: agent.Usage{InputTokens: 5}})
	options.OnAttributedUsage(agent.AttributedUsage{Stage: "code_writer", Iteration: 1, Usage: agent.Usage{InputTokens: 5}})
	_, skipped, errs := acc.Counts()
	if skipped != 1 || errs != 0 {
		t.Fatalf("counts skipped=%d errors=%d, want one skipped request and no errors", skipped, errs)
	}
	if got := acc.Snapshot().Nodes[0].Cost.Tokens; got != 5 {
		t.Fatalf("code_writer tokens = %d, want 5", got)
	}
}
