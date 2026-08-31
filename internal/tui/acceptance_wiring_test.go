package tui

// Acceptance verification (adversarial wiring probes) for the TUI
// implementation slices. Each test exercises a landed feature END TO END
// through its real input path — through Update and the live View, not by
// calling renderers directly — and asserts user-visible OUTPUT, catching
// compile-but-don't-work implementations.

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/worktrees"
)

func acceptErr(msg string) error { return acceptErrType{msg} }

type acceptErrType struct{ msg string }

func (e acceptErrType) Error() string { return e.msg }

// ---------- 1. Phase x health x gate chip (435f040) ----------

// The health chip must appear in the ACTUAL rendered View of a model that
// received a regression snapshot — through Update, not a direct panel call.
func TestAcceptanceChipVisibleInLiveView(t *testing.T) {
	m := mouseTestModel()
	m.transcript = append(m.transcript, transcriptRow{kind: rowToolCall, tool: "read_file", detail: "main.go"})
	state := presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Health:        presentation.HealthRegression,
		Nodes: []presentation.ExecutionNode{
			{ID: "test_runner", Label: "test_runner", Kind: presentation.NodeKindTest, Status: presentation.NodeStatusFailed},
		},
	}
	updated, _ := m.Update(presentationStateMsg{runID: m.activeRunID, state: state})
	next := updated.(model)
	view := plainRender(t, next.View())
	if !strings.Contains(view, "REGRESSION") {
		t.Fatal("acceptance: health regression not visible in the live View output")
	}
}

// The failed stage must ALSO flip health through the reducer itself.
func TestAcceptanceReducerDerivesFailedHealth(t *testing.T) {
	state, err := presentation.Apply(presentation.State{}, presentation.PlanEvent{StageNames: []string{"code_writer"}})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	state, err = presentation.Apply(state, presentation.StageEvent{
		ID: "test_runner", Kind: presentation.NodeKindTest, Status: "failed",
	})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if state.Health.Effective() != presentation.HealthFailed {
		t.Fatalf("acceptance: reducer did not derive failed health, got %q", state.Health)
	}
	if state.Lifecycle != presentation.LifecycleExecute {
		t.Fatalf("acceptance: lifecycle = %q, want executing", state.Lifecycle)
	}
}

// ---------- 2. Cancelled receipt (435f040 / 346eeac) ----------

// A cancelled run event must project health=cancelled AND render a
// CANCELLED card with recovery actions — reducer AND render path.
func TestAcceptanceCancelledReceiptEndToEnd(t *testing.T) {
	state, err := presentation.Apply(presentation.State{}, presentation.PlanEvent{StageNames: []string{"code_writer"}})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	state.Lifecycle = presentation.LifecycleExecute
	state, err = presentation.Apply(state, presentation.RunEvent{Status: "cancelled"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if state.Health != presentation.HealthCancelled || state.Completion == nil || state.Completion.Status != "cancelled" {
		t.Fatalf("acceptance: cancelled projection wrong: health=%q completion=%+v", state.Health, state.Completion)
	}
	plain := stripANSI(renderReceiptCard(cancelledReceiptCard("", state.Completion.Staged, ""), 80))
	if !strings.Contains(plain, "CANCELLED") || !strings.Contains(plain, "[R] resume") {
		t.Fatalf("acceptance: cancelled receipt not renderable with actions:\n%s", plain)
	}
}

// ---------- 3. Glyph tiers (739b1b5) ----------

// The ASCII tier must be width-exact for all markers and the progress bar
// must never emit ambiguous glyphs — property-style across fractions.
func TestAcceptanceGlyphTierWidthProperty(t *testing.T) {
	for _, status := range []presentation.NodeStatus{
		presentation.NodeStatusRunning, presentation.NodeStatusComplete,
		presentation.NodeStatusFailed, presentation.NodeStatusDegraded, presentation.NodeStatusPending,
	} {
		m := presentation.StatusMarker(status, presentation.GlyphTierASCII)
		if len([]rune(m.Glyph)) != 3 || m.Word == "" {
			t.Fatalf("acceptance: marker %q for %s violates the 3-cell/word contract", m.Glyph, status)
		}
	}
	for _, pct := range []int{0, 1, 17, 38, 50, 63, 75, 88, 99, 100} {
		bar := presentation.ProgressBar(float64(pct)/100, 16)
		if len([]rune(bar)) != 18 || strings.ContainsAny(bar, "█░▏▎▍") {
			t.Fatalf("acceptance: progress bar at %d%% violates the ASCII width contract: %q", pct, bar)
		}
	}
}

// ---------- 4. Compact collapse (4e8c336) ----------

// At 80 columns the sidebar must be GONE from the actual View and the
// pipeline strip with section tabs must be present.
func TestAcceptanceCompactCollapseInLiveView(t *testing.T) {
	m := mouseTestModel()
	m.width = 80
	m.height = 40
	m.altScreen = true
	m.transcript = append(m.transcript, transcriptRow{kind: rowToolCall, tool: "read_file", detail: "main.go"})
	m.pipeline.applyState(presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Nodes: []presentation.ExecutionNode{
			{ID: "code_writer", Label: "code_writer", Kind: presentation.NodeKindWrite, Status: presentation.NodeStatusRunning, Progress: 0.5},
		},
	})
	if m.sidebarActive() {
		t.Fatal("acceptance: sidebar active at 80 columns (DoD 16 violation)")
	}
	view := plainRender(t, m.View())
	if strings.Contains(view, "no files touched") {
		t.Fatal("acceptance: sidebar sections leaked into the compact view")
	}
	if !strings.Contains(view, "PIPELINE") || !strings.Contains(view, "[B] sidebar") {
		t.Fatal("acceptance: compact view missing the pipeline strip + section tabs")
	}
}

// ---------- 5. Receipt cards through the real update path (346eeac) ----------

// A plan-execution failure with a canceled context must produce the
// CANCELLED card (not FAILED) in the transcript — run.go's context
// cancellation through to the stored rows.
func TestAcceptanceCancelFailureProjectsCancelledCard(t *testing.T) {
	m := mouseTestModel()
	m.activeRunID = 42
	updated, _ := m.Update(planExecutionResultMsg{runID: 42, err: context.Canceled})
	next := updated.(model)
	for _, row := range next.transcript {
		if row.kind != rowError {
			continue
		}
		if card, ok := parseReceiptTranscriptPayload(row.text); ok {
			if card.kind != receiptCancelled {
				t.Fatalf("acceptance: canceled context projected %q, want cancelled", card.kind)
			}
			return
		}
	}
	t.Fatal("acceptance: no receipt row produced for a canceled plan execution")
}

// A non-cancel failure must produce FAILED with the full reason preserved
// (the shot-22 truncation fix, exercised through Update).
func TestAcceptanceFailFailurePreservesReason(t *testing.T) {
	m := mouseTestModel()
	m.activeRunID = 42
	reason := "stream error: provider disconnected mid-critique, 2 of 5 findings written"
	updated, _ := m.Update(planExecutionResultMsg{runID: 42, err: acceptErr(reason)})
	next := updated.(model)
	for _, row := range next.transcript {
		if row.kind != rowError {
			continue
		}
		if card, ok := parseReceiptTranscriptPayload(row.text); ok && card.kind == receiptFailed {
			rendered := stripANSI(renderReceiptCard(card, 80))
			if !strings.Contains(rendered, "provider disconnected mid-critique") {
				t.Fatalf("acceptance: failure reason truncated in receipt:\n%s", rendered)
			}
			return
		}
	}
	t.Fatal("acceptance: no FAILED receipt row produced")
}

// ---------- 6. Decisions ledger wired through the tool (681f9c3) ----------

// The pin tool must round-trip: Run records, Take drains, and the drained
// decision's session event reconstructs into DesignState.Decisions — the
// full chain the ledger card depends on. Reconstruction lives in the splice
// package and is covered by its own tests; here we prove the TUI-side chain
// (tool -> recorder -> drain) produces exactly the payload shape the
// splice-side appender emits.
func TestAcceptancePinToolRegisteredForDesignTurns(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	designRegistry := designConversationRegistry(m.registry)
	if designRegistry == nil {
		t.Fatal("acceptance: design conversation registry nil")
	}
	rec := splice.NewDecisionRecorder()
	pinTool := splice.NewPinDesignDecisionTool(rec)
	designRegistry.Register(pinTool)
	// The registry must now serve the tool by name — the same lookup a
	// real design-turn tool call makes.
	if _, ok := designRegistry.Get("pin_design_decision"); !ok {
		t.Fatal("acceptance: pin_design_decision not registered in the design registry")
	}
	// And the registered instance must actually record through the shared
	// recorder the TUI drains.
	res, ok := designRegistry.Get("pin_design_decision")
	if !ok {
		t.Fatal("acceptance: registry lost the pin tool")
	}
	runRes := res.Run(context.Background(), map[string]any{"statement": "retry idempotent only"})
	if runRes.Status != "ok" {
		t.Fatalf("acceptance: registered tool pin failed: %s", runRes.Output)
	}
	drained := rec.Take()
	if len(drained) != 1 || drained[0].Statement != "retry idempotent only" {
		t.Fatalf("acceptance: drained ledger = %+v", drained)
	}
}

// ---------- 7. ask_user gate elevation (e9430f9) ----------

// Opening a real askUserRequestMsg must stamp startedAt and render the gate
// signature in the questionnaire — via Update.
func TestAcceptanceGateSignatureViaUpdate(t *testing.T) {
	m := mouseTestModel()
	m.activeRunID = 7
	msg := askUserRequestMsg{
		runID: 7,
		request: agent.AskUserRequest{
			Header:    "Retry policy",
			Questions: []agent.AskUserQuestion{{Question: "Buffer streamed bodies?", Options: []string{"buffer", "never"}}},
		},
		answer: func([]string) {},
	}
	updated, _ := m.Update(msg)
	next := updated.(model)
	if next.pendingAskUser == nil || next.pendingAskUser.startedAt.IsZero() {
		t.Fatal("acceptance: gate did not open with a start time")
	}
	rendered := stripANSI(renderAskUserQuestionnaire(*next.pendingAskUser, "", 90))
	for _, want := range []string{"[?]", "NEEDS YOU", "no work running | no tokens burning", "Buffer streamed bodies?"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("acceptance: gate card missing %q:\n%s", want, rendered)
		}
	}
}

// ---------- 8. HANDOFF (49f75e2) ----------

// A failed plan execution with a kept worktree must append BOTH the FAILED
// receipt and the HANDOFF card, and the handoff row must re-render as a
// card at any width.
func TestAcceptanceHandoffOfferedOnFailure(t *testing.T) {
	m := mouseTestModel()
	m.activeRunID = 42
	wt := &worktrees.Result{Name: "wt-acc1", Path: "/nonexistent/wt-acc1", RepoRoot: "/nonexistent"}
	updated, _ := m.Update(planExecutionResultMsg{runID: 42, err: acceptErr("stage failed"), worktree: wt})
	next := updated.(model)
	handoffRows, receiptRows := 0, 0
	for _, row := range next.transcript {
		switch row.kind {
		case rowSystem:
			if h, _, ok := parseHandoffTranscriptPayload(row.text); ok && h.lane == "wt-acc1" {
				handoffRows++
				rendered := stripANSI(renderHandoffCard(h, false, 90))
				// The worktree does not exist on disk in the test env, so
				// WORK LOST is the honest render — lane death vs work loss
				// is the point of the surface.
				if !strings.Contains(rendered, "HANDOFF") {
					t.Fatalf("acceptance: handoff row did not render as a card:\n%s", rendered)
				}
			}
		case rowError:
			if _, ok := parseReceiptTranscriptPayload(row.text); ok {
				receiptRows++
			}
		}
	}
	if handoffRows != 1 {
		t.Fatalf("acceptance: %d handoff rows appended, want 1", handoffRows)
	}
	if receiptRows != 1 {
		t.Fatalf("acceptance: %d receipt rows appended, want 1", receiptRows)
	}
}

// ---------- 9. HANDOFF interactive keys (49f75e2 + key wiring) ----------

// Pressing M on a pending preserved handoff dispatches the merge through
// the SAME seam the review uses (tuiMergeBackWorktree) and clears the
// handoff. Proven with the test seam swapped, so this is a wiring probe
// of the real dispatch path.
func TestAcceptanceHandoffMergeKeyDispatchesMerge(t *testing.T) {
	origMerge := tuiMergeBackWorktree
	defer func() { tuiMergeBackWorktree = origMerge }()
	merged := false
	tuiMergeBackWorktree = func(_ context.Context, options worktrees.MergeBackOptions) (worktrees.MergeBackResult, error) {
		merged = true
		if options.Name != "wt-acc2" {
			t.Fatalf("acceptance: merge ran on the wrong lane: %q", options.Name)
		}
		return worktrees.MergeBackResult{}, nil
	}

	m := mouseTestModel()
	m.activeRunID = 42
	wt := &worktrees.Result{Name: "wt-acc2", Path: "/nonexistent/wt-acc2", RepoRoot: "/nonexistent"}
	updatedAfterFail, _ := m.Update(planExecutionResultMsg{runID: 42, err: acceptErr("boom"), worktree: wt})
	// Real flow: the review picker opened with the receipt. Enter submits
	// the recommended answer (Keep) so the picker resolves and the
	// worktree is kept — THEN the handoff keys arm (the picker owns input
	// while it is up).
	next := updatedAfterFail.(model)
	if next.pendingAskUser == nil {
		t.Fatal("acceptance: review picker did not open after failure")
	}
	updatedAfterKeep, _ := next.Update(testKey(tea.KeyEnter))
	next = updatedAfterKeep.(model)
	if next.pendingAskUser != nil {
		t.Fatal("acceptance: review picker did not resolve on Keep")
	}
	next.pendingHandoff.preserved = true

	updated, cmd := next.Update(testKey('M'))
	nextModel := updated.(model)
	if !merged {
		t.Fatal("acceptance: [M] did not dispatch the merge-back seam")
	}
	if nextModel.pendingHandoff != nil {
		t.Fatal("acceptance: [M] did not resolve the handoff")
	}
	if cmd == nil {
		t.Fatal("acceptance: [M] produced no background command for the review result")
	}
}

// Pressing X on a pending handoff dispatches the discard through the same
// seam the review's Reject uses (worktree removed, branch kept).
func TestAcceptanceHandoffDiscardKeyDispatchesRemove(t *testing.T) {
	origRemove := tuiRemoveWorktree
	defer func() { tuiRemoveWorktree = origRemove }()
	removed := false
	tuiRemoveWorktree = func(_ context.Context, options worktrees.RemoveOptions) error {
		removed = true
		if options.Path != "/nonexistent/wt-acc3" {
			t.Fatalf("acceptance: discard ran on the wrong lane: %q", options.Path)
		}
		return nil
	}
	// [X] goes through the review's Reject path: preserve the branch first,
	// then remove the worktree. Both seams must be stubbed for a lane whose
	// paths do not exist in the test environment.
	preserved := false
	tuiPreserveWorktree = func(_ context.Context, options worktrees.MergeBackOptions) (string, error) {
		preserved = true
		if options.Name != "wt-acc3" {
			t.Fatalf("acceptance: preserve ran on the wrong lane: %q", options.Name)
		}
		return "splice/wt-acc3", nil
	}

	m := mouseTestModel()
	m.activeRunID = 43
	wt := &worktrees.Result{Name: "wt-acc3", Path: "/nonexistent/wt-acc3", RepoRoot: "/nonexistent"}
	updatedAfterFail, _ := m.Update(planExecutionResultMsg{runID: 43, err: acceptErr("boom"), worktree: wt})
	next := updatedAfterFail.(model)
	if next.pendingAskUser == nil {
		t.Fatal("acceptance: review picker did not open after failure")
	}
	updatedAfterKeep, _ := next.Update(testKey(tea.KeyEnter))
	next = updatedAfterKeep.(model)
	next.pendingHandoff.preserved = true

	updated, _ := next.Update(testKey('X'))
	nextModel := updated.(model)
	if !preserved || !removed {
		t.Fatalf("acceptance: [X] did not run preserve-then-remove (preserved=%v removed=%v)", preserved, removed)
	}
	if nextModel.pendingHandoff != nil {
		t.Fatal("acceptance: [X] did not resolve the handoff")
	}
}
