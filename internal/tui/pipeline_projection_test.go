package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/presentation"
)

// ---- Shared projection fixtures ----

// pendingRosterState builds an execute-phase state with the given nodes all
// pending, in roster order.
func pendingRosterState(names ...string) presentation.State {
	state := presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Plan:          presentation.Plan{Title: "hello service", TaskCount: len(names)},
	}
	for _, name := range names {
		state.Nodes = append(state.Nodes, presentation.ExecutionNode{
			ID: name, Label: name, Kind: presentation.NodeKindCustom, Status: presentation.NodeStatusPending,
		})
	}
	return state
}

// midRunProjectionState is the standard mixed-status mid-run state: one
// complete, one running at 50%, one pending, with one file change.
func midRunProjectionState() presentation.State {
	return presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Plan:          presentation.Plan{Title: "hello service", TaskCount: 3},
		Nodes: []presentation.ExecutionNode{
			{ID: "code_writer", Label: "code_writer", Kind: presentation.NodeKindWrite, Status: presentation.NodeStatusComplete, Progress: 1},
			{ID: "test_runner", Label: "test_runner", Kind: presentation.NodeKindTest, Status: presentation.NodeStatusRunning, Progress: 0.5},
			{ID: "acceptance_verifier", Label: "acceptance_verifier", Kind: presentation.NodeKindVerify, Status: presentation.NodeStatusPending},
		},
		Files: []presentation.FileChangeSummary{{Path: "main.go", Status: "modified"}},
	}
}

// approveProjectionState is the snapshot the approve-path wiring test feeds:
// code_writer running, two stages pending.
func approveProjectionState() presentation.State {
	return presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Plan:          presentation.Plan{Title: "hello service", TaskCount: 3},
		Nodes: []presentation.ExecutionNode{
			{ID: "code_writer", Label: "code_writer", Kind: presentation.NodeKindWrite, Status: presentation.NodeStatusRunning, Progress: 0.3},
			{ID: "test_runner", Label: "test_runner", Kind: presentation.NodeKindTest, Status: presentation.NodeStatusPending},
			{ID: "acceptance_verifier", Label: "acceptance_verifier", Kind: presentation.NodeKindVerify, Status: presentation.NodeStatusPending},
		},
	}
}

// failedNodeProjectionState is a failed test_runner with a proposed rollback
// intervention (the repair-loop revision request), pinned by the golden.
func failedNodeProjectionState() presentation.State {
	return presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Plan:          presentation.Plan{TaskCount: 2},
		Nodes: []presentation.ExecutionNode{
			{ID: "code_writer", Label: "code_writer", Kind: presentation.NodeKindWrite, Status: presentation.NodeStatusComplete, Progress: 1},
			{ID: "test_runner", Label: "test_runner", Kind: presentation.NodeKindTest, Status: presentation.NodeStatusFailed, Progress: 1},
		},
		Interventions: []presentation.Intervention{
			{Kind: presentation.InterventionRollback, Reason: "revision_request -> code_writer: 2 failing tests", TargetNodeID: "test_runner", Status: presentation.InterventionProposed},
		},
	}
}

// completedRunProjectionState is a fully complete run with files and receipt.
func completedRunProjectionState() presentation.State {
	return presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleComplete,
		Plan:          presentation.Plan{Title: "hello service", TaskCount: 3},
		Nodes: []presentation.ExecutionNode{
			{ID: "code_writer", Label: "code_writer", Kind: presentation.NodeKindWrite, Status: presentation.NodeStatusComplete, Progress: 1},
			{ID: "test_runner", Label: "test_runner", Kind: presentation.NodeKindTest, Status: presentation.NodeStatusComplete, Progress: 1},
			{ID: "security_auditor", Label: "security_auditor", Kind: presentation.NodeKindSecurity, Status: presentation.NodeStatusComplete, Progress: 1},
		},
		Files: []presentation.FileChangeSummary{
			{Path: "main.go", Status: "modified", Additions: 34, Deletions: 2},
			{Path: "main_test.go", Status: "added", Additions: 58, Deletions: 0},
		},
		Completion: &presentation.CompletionReceipt{Status: "completed", Detail: "all gates green"},
	}
}

// adversarialTopologyState is the D2 fixture: stages the TUI has never heard
// of, with kinds outside the known constants (valid per the open-set
// NodeKind.Validate). Rendering must be driven purely by Kind metadata, so
// this topology renders with zero TUI changes.
func adversarialTopologyState() presentation.State {
	return presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Plan:          presentation.Plan{Title: "release pipeline", TaskCount: 3},
		Nodes: []presentation.ExecutionNode{
			{ID: "deploy_hook", Label: "deploy_hook", Kind: "DATA_TRANSFORM", Status: presentation.NodeStatusRunning, Progress: 0.5},
			{ID: "release_gate", Label: "release_gate", Kind: presentation.NodeKindReview, Status: presentation.NodeStatusPending},
			{ID: "sync_worker", Label: "sync_worker", Kind: "DATA_PIPELINE", Status: presentation.NodeStatusComplete, Progress: 1},
		},
	}
}

// repairProjectionState is the D4 fixture: a repair loop in flight. The node
// is running again after a failure and the repair story lives in the
// interventions (proposed rollback, then applied continue). A naive
// derivation from raw events could read this sequence as a failure; the
// state carries the truth.
func repairProjectionState() presentation.State {
	return presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Plan:          presentation.Plan{TaskCount: 3},
		Nodes: []presentation.ExecutionNode{
			{ID: "code_writer", Label: "code_writer", Kind: presentation.NodeKindWrite, Status: presentation.NodeStatusComplete, Progress: 1},
			{ID: "test_runner", Label: "test_runner", Kind: presentation.NodeKindTest, Status: presentation.NodeStatusRunning, Progress: 0.5},
			{ID: "acceptance_verifier", Label: "acceptance_verifier", Kind: presentation.NodeKindVerify, Status: presentation.NodeStatusPending},
		},
		Interventions: []presentation.Intervention{
			{Kind: presentation.InterventionRollback, Reason: "revision_request -> code_writer: 2 failing tests", TargetNodeID: "test_runner", Status: presentation.InterventionProposed},
			{Kind: presentation.InterventionContinue, Reason: "revision resolved: tests pass", TargetNodeID: "test_runner", Status: presentation.InterventionApplied},
		},
	}
}

// ---- D3: golden rendering tests ----

// The golden helper implements the P1.0 pattern (internal/presentation/
// golden_test.go): files live under testdata/, captured with
// SPLICE_UPDATE_GOLDEN=1, and reviewed in diffs. The capture is the plain
// (ANSI-stripped) renderSection output, which is the panel body; the header
// count/color and strip surfaces are pinned by the non-golden panel and
// strip tests.

func tuiGoldenPath(name string) string {
	return filepath.Join("testdata", name+".golden")
}

func tuiUpdateGolden() bool {
	return os.Getenv("SPLICE_UPDATE_GOLDEN") == "1"
}

func checkPipelineGolden(t *testing.T, name string, state presentation.State, width int) {
	t.Helper()
	var panel pipelinePanelState
	panel.applyState(state)
	rendered := strings.Join(panel.renderSection(width, 0), "\n")
	plain := ansiPattern.ReplaceAllString(rendered, "")
	encoded := []byte(plain + "\n")
	path := tuiGoldenPath(name)
	if tuiUpdateGolden() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create testdata dir: %v", err)
		}
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("updated golden %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (create it with SPLICE_UPDATE_GOLDEN=1)", path, err)
	}
	if !bytes.Equal(want, encoded) {
		t.Fatalf("golden %s mismatch (regenerate with SPLICE_UPDATE_GOLDEN=1):\n--- want\n%s--- got\n%s", name, want, encoded)
	}
}

func TestPipelineGoldenRenders(t *testing.T) {
	fixtures := []struct {
		name  string
		state presentation.State
	}{
		{"empty_state", presentation.State{SchemaVersion: presentation.PresentationSchemaVersionV1}},
		{"mid_run", midRunProjectionState()},
		{"failed_node_with_intervention", failedNodeProjectionState()},
		{"completed_run_with_receipt", completedRunProjectionState()},
		{"adversarial_topology", adversarialTopologyState()},
	}
	for _, fixture := range fixtures {
		for _, width := range []int{80, 120} {
			t.Run(fmt.Sprintf("%s_w%d", fixture.name, width), func(t *testing.T) {
				checkPipelineGolden(t, fmt.Sprintf("pipeline_%s_w%d", fixture.name, width), fixture.state, width)
			})
		}
	}
}

// ---- D4: regression proof ----

// TestProjectionShowsRepairTruthFromState proves why the projection switch
// happened: an event sequence a naive derivation could read as a failure
// (test_runner failed, then a repair message, then re-entry) is projected
// from the state as a node RUNNING with the repair interventions listed. The
// old derivation could disagree with the runtime about whether the run was
// failing; the projection cannot.
func TestProjectionShowsRepairTruthFromState(t *testing.T) {
	var panel pipelinePanelState
	panel.applyState(repairProjectionState())
	plain := plainRender(t, strings.Join(panel.renderSection(80, 0), "\n"))

	if !strings.Contains(plain, "◜") {
		t.Fatalf("repair node not rendered as running: %q", plain)
	}
	if !strings.Contains(plain, "test_runner") {
		t.Fatalf("repair node missing from roster: %q", plain)
	}
	if !strings.Contains(plain, "revision_request") {
		t.Fatalf("repair intervention missing from messages: %q", plain)
	}
	if strings.Contains(plain, "✗") {
		t.Fatalf("naive failure leak: repair rendered as failed: %q", plain)
	}

	p := panel.presentation()
	if p.current == nil || p.current.name != "test_runner" || !p.current.reentered {
		t.Fatalf("repair stage not current/reentered: %#v", p.current)
	}
	if p.stripState() != pipelineStripRepair {
		t.Fatalf("strip state = %d, want repair", p.stripState())
	}
}

// TestKindTagPinsKnownAndMadeUpKinds is the D2 table pin: the kind tag is
// derived from Kind metadata for both known constants and kinds outside the
// known set, so an unknown topology renders without TUI changes.
func TestKindTagPinsKnownAndMadeUpKinds(t *testing.T) {
	cases := []struct {
		kind presentation.NodeKind
		want string
	}{
		{presentation.NodeKindWrite, "w"},
		{presentation.NodeKindAnalyze, "a"},
		{presentation.NodeKindSecurity, "s"},
		{presentation.NodeKindLint, "l"},
		{presentation.NodeKindCustom, "c"},
		{presentation.NodeKindTest, "t"},
		{presentation.NodeKindVerify, "v"},
		{presentation.NodeKindReview, "r"},
		// Kinds outside the known constants (open-set valid):
		{"DATA_TRANSFORM", "dt"},
		{"DATA_PIPELINE", "dp"},
		{"RELEASE_GATE", "rg"},
		{"MULTI_PART_KIND", "mpk"},
		// Unknown or empty formats fall back to "?".
		{"", "?"},
		{"_", "?"},
		{"__", "?"},
	}
	for _, tc := range cases {
		if got := kindTag(tc.kind); got != tc.want {
			t.Fatalf("kindTag(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

// TestPipelineStageRowCarriesNoModelField is the D3 compile-time pin: the
// panel state deliberately has no per-stage model field. The stage model
// wizard is the model-selection surface; adding Model to the row would open
// a panel model column the state does not carry.
func TestPipelineStageRowCarriesNoModelField(t *testing.T) {
	typ := reflect.TypeOf(pipelineStageRow{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Name == "Model" {
			t.Fatal("pipelineStageRow must not carry a model field: per-stage model display belongs to the stage model wizard")
		}
	}
}
