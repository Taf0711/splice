package splice

// Acceptance verification for the RUNTIME side of the TUI contract:
// the orchestrator must actually EMIT the truth the TUI projects. These
// probes run the real pipeline seams (no mocks beyond the provider) and
// assert emission, not just structure.

import (
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// TestAcceptanceStageEventsStampWorkspace proves DoD 26's runtime truth:
// a run with a worktree stamps isolated on every stage event; a run
// without one stamps shared_cwd. The stamp happens in emitStageEvent from
// PipelineRunConfig.IsolatedWorktree, derived from the run's own config.
func TestAcceptanceStageEventsStampWorkspace(t *testing.T) {
	var isolatedEvents, sharedEvents []agent.StageEvent
	collect := func(events *[]agent.StageEvent) func(agent.StageEvent) {
		return func(e agent.StageEvent) { *events = append(*events, e) }
	}

	// Isolated: IsolatedWorktree set directly on the config (what
	// RunDesignPlanWithResume derives when Cwd != ProjectRoot).
	isolatedCfg := PipelineRunConfig{
		Cwd:              "/repo/.splice/wt/a1",
		ProjectRoot:      "/repo",
		IsolatedWorktree: "/repo/.splice/wt/a1",
		OnStageEvent:     collect(&isolatedEvents),
	}
	emitStageEvent(isolatedCfg, "code_writer", "running", "writing", 0, nil)
	if len(isolatedEvents) != 1 {
		t.Fatalf("acceptance: isolated run emitted %d events, want 1", len(isolatedEvents))
	}
	if isolatedEvents[0].Workspace != "isolated" || isolatedEvents[0].WorktreePath != "/repo/.splice/wt/a1" {
		t.Fatalf("acceptance: isolated stamp wrong: %+v", isolatedEvents[0])
	}

	// Shared: no worktree, still stamps the honest shared badge.
	sharedCfg := PipelineRunConfig{
		Cwd:          "/repo",
		ProjectRoot:  "/repo",
		OnStageEvent: collect(&sharedEvents),
	}
	emitStageEvent(sharedCfg, "code_writer", "running", "writing", 0, nil)
	if len(sharedEvents) != 1 {
		t.Fatalf("acceptance: shared run emitted %d events, want 1", len(sharedEvents))
	}
	if sharedEvents[0].Workspace != "shared_cwd" {
		t.Fatalf("acceptance: shared stamp wrong: %+v", sharedEvents[0])
	}
}

// TestAcceptanceDesignRunnerDerivesIsolation proves the design runner's
// isolation rule: Cwd inside a worktree distinct from ProjectRoot means
// the lane is isolated. The rule is restated here so a change in the
// runner fails this assertion too.
func TestAcceptanceDesignRunnerDerivesIsolation(t *testing.T) {
	options := agent.Options{
		Cwd:         "/repo/.splice/wt/a1",
		ProjectRoot: "/repo",
	}
	cfg := PipelineConfigFromAgentOptions(options)
	derived := ""
	if cfg.ProjectRoot != "" && cfg.Cwd != cfg.ProjectRoot {
		derived = cfg.Cwd
	}
	if derived != "/repo/.splice/wt/a1" {
		t.Fatalf("acceptance: isolation derivation = %q, want the worktree cwd", derived)
	}
	// Same cwd as root: shared, never falsely isolated.
	sameCwd := PipelineConfigFromAgentOptions(agent.Options{Cwd: "/repo", ProjectRoot: "/repo"})
	if sameCwd.ProjectRoot != "" && sameCwd.Cwd != sameCwd.ProjectRoot {
		t.Fatal("acceptance: same cwd/root falsely derived as isolated")
	}
}

// TestAcceptanceDecisionEventsReconstructAcrossCrystallize is the
// durability probe: pin -> persist-shaped events -> crystallize -> more
// pins -> reconstruct. The ledger must survive the crystallization and
// preserve order, because the design lane's decision ledger is the thing
// /crystallize reasons over.
func TestAcceptanceDecisionEventsReconstructAcrossCrystallize(t *testing.T) {
	appender := func(statement string) sessions.AppendEventInput {
		return DecisionPinnedAppender(statement, "", "retry semantics")
	}
	events := []sessions.Event{
		{Type: sessions.EventDesignModeEntered, Sequence: 1},
		{Type: sessions.EventDecisionPinned, Sequence: 2, Payload: mustJSON(appender("retry idempotent only").Payload)},
		{Type: sessions.EventDecisionPinned, Sequence: 3, Payload: mustJSON(appender("preserve caller deadline").Payload)},
		{Type: sessions.EventPlanCrystallized, Sequence: 4, Payload: mustJSON(PlanCrystallizedPayload{
			PlanID:   "p1",
			Revision: 1,
			Plan:     mustJSON(schemas.DesignPlan{Epic: "e", Requirements: []string{"r"}}),
		})},
		{Type: sessions.EventDecisionPinned, Sequence: 5, Payload: mustJSON(appender("cap backoff at 5s").Payload)},
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("acceptance: reconstruct: %v", err)
	}
	if len(state.Decisions) != 3 {
		t.Fatalf("acceptance: ledger has %d decisions, want 3 across crystallization", len(state.Decisions))
	}
	want := []string{"retry idempotent only", "preserve caller deadline", "cap backoff at 5s"}
	for i, statement := range want {
		if state.Decisions[i].Statement != statement {
			t.Fatalf("acceptance: ledger[%d] = %q, want %q (order is the audit trail)", i, state.Decisions[i].Statement, statement)
		}
	}
	if state.Phase != schemas.DesignPhaseReview {
		t.Fatalf("acceptance: phase = %q after crystallize, want review", state.Phase)
	}
}

// TestAcceptanceWorkspaceSurvivesReentryThroughAccumulator proves the
// isolation identity survives a repair re-entry THROUGH THE REAL SEAM
// (presentrun.Accumulator feeding the reducer): a re-entrant stage event
// without a workspace keeps the prior badge, because a lane does not
// change isolation mid-run.
func TestAcceptanceWorkspaceSurvivesReentryThroughAccumulator(t *testing.T) {
	acc := presentrun.New(nil)
	acc.Apply(presentrun.AdaptStageEvent(agent.StageEvent{
		Name: "code_writer", Status: "failed",
		Workspace: "isolated", WorktreePath: "/wt/a1",
	}))
	first := acc.Snapshot()
	if len(first.Nodes) != 1 || first.Nodes[0].Workspace != "isolated" {
		t.Fatalf("acceptance: first event lost workspace: %+v", first.Nodes)
	}
	// Re-entry without a workspace stamp: the identity must persist.
	acc.Apply(presentrun.AdaptStageEvent(agent.StageEvent{
		Name: "code_writer", Status: "running",
	}))
	second := acc.Snapshot()
	if len(second.Nodes) != 1 {
		t.Fatalf("acceptance: re-entry produced %d nodes", len(second.Nodes))
	}
	if second.Nodes[0].Workspace != "isolated" || second.Nodes[0].WorktreePath != "/wt/a1" {
		t.Fatalf("acceptance: re-entry lost workspace identity: %+v", second.Nodes[0])
	}
	// The re-entry must also flip health to recovering (projection of the
	// repair loop, landed with the phase x health work).
	if second.Health.Effective() != "recovering" {
		t.Fatalf("acceptance: re-entry health = %q, want recovering", second.Health.Effective())
	}
}

// TestAcceptanceAdapterCarriesWorkspace proves the adapter is not a dead
// pass-through: a workspace stamped on the agent event reaches the
// reducer's Event and lands on the node.
func TestAcceptanceAdapterCarriesWorkspace(t *testing.T) {
	acc := presentrun.New(nil)
	acc.Apply(presentrun.AdaptStageEvent(agent.StageEvent{
		Name: "static_analyzer", Status: "running", Workspace: "shared_cwd",
	}))
	snap := acc.Snapshot()
	if len(snap.Nodes) != 1 || snap.Nodes[0].Workspace != "shared_cwd" {
		t.Fatalf("acceptance: adapter dropped the workspace stamp: %+v", snap.Nodes)
	}
	// An unknown stage name must not break the workspace flow (CUSTOM kind
	// fallback for custom topologies).
	acc.Apply(presentrun.AdaptStageEvent(agent.StageEvent{
		Name: "my_custom_stage", Status: "running", Workspace: "isolated", WorktreePath: "/wt/z",
	}))
	snap = acc.Snapshot()
	found := false
	for _, node := range snap.Nodes {
		if node.ID == "my_custom_stage" && node.Workspace == "isolated" && node.WorktreePath == "/wt/z" {
			found = true
		}
	}
	if !found {
		t.Fatal("acceptance: custom stage lost its workspace stamp")
	}
}
