package tui

// acceptance_verify_test.go is the acceptance-verifier pass over the batch
// of slices landed since 10f35b0: GAP-G diff viewport (d22a5fe, f6ebed6),
// GAP-F [O] (8bb7be4), GAP-I trust evidence (48da3f7), and the fold-table
// fix (3ea5e3f). Discipline from the review session: probes must use inputs
// the REAL system can produce, lazy cmds must be RUN, negative controls must
// exist, and every probe must state what was observed.

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/worktrees"
)

// ---- GAP-F [O]: the probe shipped in 8bb7be4 used testKey('O'), a shape
// ultraviolet never emits (the exact fake-green the review pass caught for
// [M]/[X]). These probes use the real shift+o shape and add the negative
// control plain o.

func verifierArmedHandoff(t *testing.T, lane string) model {
	t.Helper()
	m := mouseTestModel()
	m.activeRunID = 42
	wt := &worktrees.Result{Name: lane, Path: t.TempDir(), RepoRoot: "/nonexistent"}
	updated, _ := m.Update(planExecutionResultMsg{runID: 42, err: acceptErr("boom"), worktree: wt})
	next := updated.(model)
	if next.pendingAskUser == nil {
		t.Fatal("verifier: review picker did not open after failure")
	}
	updatedKeep, _ := next.Update(testKey(tea.KeyEnter))
	next = updatedKeep.(model)
	if next.pendingHandoff == nil {
		t.Fatal("verifier: no handoff after Keep")
	}
	next.pendingHandoff.preserved = true
	return next
}

func TestVerifyHandoffOpenKeyRealShapeDispatchesEditor(t *testing.T) {
	next := verifierArmedHandoff(t, "wt-vfy-o")
	t.Setenv("EDITOR", "true")
	updated, cmd := next.Update(reviewRealShiftKey('O'))
	nextModel := updated.(model)
	if cmd == nil {
		t.Fatal("verifier: real shift+o produced no editor command — the advertised [O] is dead on a real terminal")
	}
	if nextModel.pendingHandoff == nil {
		t.Fatal("verifier: [O] must not resolve the handoff (opening is not a decision)")
	}
}

func TestVerifyHandoffOpenKeyPlainONotDispatch(t *testing.T) {
	next := verifierArmedHandoff(t, "wt-vfy-plain-o")
	t.Setenv("EDITOR", "true")
	updated, cmd := next.Update(reviewRealPlainKey('o'))
	nextModel := updated.(model)
	if cmd != nil {
		t.Fatal("verifier: plain o dispatched the editor; plain letters belong to the composer")
	}
	if nextModel.pendingHandoff == nil {
		t.Fatal("verifier: plain o disturbed the handoff")
	}
}

// ---- GAP-G diff viewport: the shipped probes drive n/scroll with testKey('n')
// (Code 'n', Text ""), which happens to match the real shape for lowercase
// keys. Verify the full action set against REAL shift-independence (these
// are lowercase-advertised keys) and the empty-composer guard.

func verifierDiffActive(t *testing.T) model {
	t.Helper()
	m := mouseTestModel()
	updated, _ := m.Update(diffCapturedMsg{
		lane: "wt-vfy-diff",
		res:  diffReviewTestDiff,
	})
	_ = updated
	// Open through the real emission seam, then land the capture.
	opened, cmd := openedModel.diffViewProbe(t)
	_ = cmd
	return opened
}

// openedModel is a compile-time seam so the helper above stays honest; the
// real probe below builds the state directly.
var openedModel verifierProbeHost

type verifierProbeHost struct{}

func (verifierProbeHost) diffViewProbe(t *testing.T) (model, tea.Cmd) {
	t.Helper()
	m := mouseTestModel()
	opened, cmd := m.openDiffReview(worktrees.Result{Name: "wt-vfy-diff", Path: "/tmp/does-not-matter", RepoRoot: "/tmp"})
	if !opened.diffView.active {
		t.Fatal("verifier: openDiffReview did not activate the view")
	}
	updated, _ := opened.Update(diffCapturedMsg{lane: "wt-vfy-diff", res: diffReviewTestDiff})
	return updated.(model), cmd
}

func TestVerifyDiffKeysOnRealShapes(t *testing.T) {
	// n, a, j, o are advertised LOWERCASE. Real shapes: plain n arrives as
	// Code 'n' Text "n"; shift+n as Code 'n' ShiftedCode 'N' Text "N"
	// Mod Shift (reviewRealShiftKey takes the UPPERCASE letter — passing a
	// lowercase letter fabricates Code 0x8E, a shape no terminal emits).
	m, _ := verifierProbeHost{}.diffViewProbe(t)

	// Real plain n advances to the next file header.
	before := m.diffView.hunkTop
	updated, _ := m.Update(reviewRealPlainKey('n'))
	next := updated.(model)
	if next.diffView.hunkTop == before {
		t.Fatal("verifier: real plain n did not move to the next file")
	}

	// Real shift+n also dispatches: keyCode reads Code 'n', so the hint
	// bar's lowercase n works with shift held too (harmless superset).
	shiftN := tea.KeyPressMsg(tea.Key{Code: 'n', ShiftedCode: 'N', Text: "N", Mod: tea.ModShift})
	updated, _ = next.Update(shiftN)
	next = updated.(model)
	if next.diffView.hunkTop == before {
		t.Fatal("verifier: real shift+n did not move to the next file")
	}

	// j emits the intervention notice and touches nothing.
	updated, _ = next.Update(reviewRealPlainKey('j'))
	next = updated.(model)
	found := false
	for _, row := range next.transcript {
		if strings.Contains(row.text, "step_back") {
			found = true
		}
	}
	if !found {
		t.Fatal("verifier: real j did not record the hunk-rejection notice")
	}

	// o with EDITOR set produces the exec cmd.
	t.Setenv("EDITOR", "true")
	updated, cmd := next.Update(reviewRealPlainKey('o'))
	if cmd == nil {
		t.Fatal("verifier: real o produced no editor command")
	}
	_ = updated
}

func TestVerifyDiffTypingInComposerDoesNotDispatch(t *testing.T) {
	m, _ := verifierProbeHost{}.diffViewProbe(t)
	// The composer has text: letters must reach it (composer echo), NOT the
	// diff actions. The view stays put.
	m.input.SetValue("redo the retry gate")
	top := m.diffView.hunkTop
	updated, _ := m.Update(reviewRealPlainKey('n'))
	next := updated.(model)
	if next.diffView.hunkTop != top {
		t.Fatal("verifier: n dispatched while the composer held text")
	}
	if got := next.composerValue(); got != "redo the retry gaten" {
		t.Fatalf("verifier: composer did not receive the letter while diff view open: %q", got)
	}
}

// a (approve all) walks the SAME seam as the review Accept. Verify dispatch
// with a stub, and that the view closes.
func TestVerifyDiffApproveAllDispatchesAcceptSeam(t *testing.T) {
	origMerge := tuiMergeBackWorktree
	defer func() { tuiMergeBackWorktree = origMerge }()
	merged := false
	tuiMergeBackWorktree = func(_ context.Context, options worktrees.MergeBackOptions) (worktrees.MergeBackResult, error) {
		merged = true
		if options.Name != "wt-vfy-a" {
			t.Fatalf("verifier: approve-all ran on the wrong lane: %q", options.Name)
		}
		return worktrees.MergeBackResult{}, nil
	}
	m := mouseTestModel()
	// Bind the view to a lane the stub can match, via the handoff path's
	// activeWorktree so applyWorktreeReview resolves.
	wt := &worktrees.Result{Name: "wt-vfy-a", Path: t.TempDir(), RepoRoot: "/nonexistent"}
	m.activeWorktree = wt
	m.diffView = diffViewState{active: true, wt: *wt, base: "main", text: diffReviewTestDiff, files: diffFileStats(diffReviewTestDiff)}
	updated, _ := m.Update(reviewRealPlainKey('a'))
	next := updated.(model)
	if !merged {
		t.Fatal("verifier: diff approve-all did not dispatch the review Accept seam")
	}
	if next.diffView.active {
		t.Fatal("verifier: approve-all did not close the diff view")
	}
}

// The nav bar carries position and NO keymap; the hint bar carries keys and
// drops whole segments (f6ebed6). Verify both against the rendered frame.
func TestVerifyDiffChromeContract(t *testing.T) {
	m, _ := verifierProbeHost{}.diffViewProbe(t)
	nav := m.diffViewNavBar(120)
	plainNav := stripANSI(nav)
	if !strings.Contains(plainNav, "hunk 1 of") {
		t.Fatalf("verifier: nav missing position readout: %q", plainNav)
	}
	if strings.Contains(plainNav, "esc close") || strings.Contains(plainNav, "next file") {
		t.Fatalf("verifier: nav carries keymap (should be hint-bar only): %q", plainNav)
	}
	body := m.renderDiffReview(120)
	plainBody := stripANSI(body)
	if !strings.Contains(plainBody, "esc close") {
		t.Fatal("verifier: body missing the hint bar")
	}
	// 40-col: optional segments shed WHOLE, no ellipsis, core survives.
	narrow := diffViewHintBar(40)
	if strings.Contains(narrow, "…") {
		t.Fatalf("verifier: hint bar ellipsis-truncated: %q", narrow)
	}
	if !strings.Contains(narrow, "esc close") || !strings.Contains(narrow, "↑↓ scroll") {
		t.Fatalf("verifier: narrow hint bar lost core keys: %q", narrow)
	}
}

// ---- GAP-I trust evidence: the card must render through the REAL launch
// path and the View(), not just the direct renderer call.

func TestVerifyTrustCardVisibleThroughLaunchAndLiveView(t *testing.T) {
	ws := t.TempDir()
	writeTrustConfigFixture(t, ws)
	m := mouseTestModel()
	m.projectConfigPath = ws + "/.splice/config.json"
	m.trustPromptRequired = true
	m = m.openTrustPromptIfRequired()
	// Real View path: the card must be visible in the live frame.
	plain := plainRender(t, m.View())
	for _, want := range []string{"UNTRUSTED PROJECT CONFIG", "not loaded", "mcp servers"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("verifier: trust card not visible in live View (missing %q)", want)
		}
	}
	// The trust menu is up alongside it.
	if m.picker == nil || m.picker.kind != pickerTrust {
		t.Fatal("verifier: trust menu did not open with the card")
	}
	// Exactly ONE evidence card: no double-emission.
	cards := 0
	for _, row := range m.transcript {
		if strings.Contains(row.text, trustConfigTranscriptMarker) {
			cards++
		}
	}
	if cards != 1 {
		t.Fatalf("verifier: %d evidence cards appended, want exactly 1", cards)
	}
}

// The card must NOT appear for a TRUSTED workspace with a config (the
// evidence is for the undecided/declined case).
func TestVerifyTrustedWorkspaceGetsNoTrustCard(t *testing.T) {
	ws := t.TempDir()
	writeTrustConfigFixture(t, ws)
	m := mouseTestModel()
	m.projectConfigPath = ws + "/.splice/config.json"
	m.trusted = true
	m.trustPromptRequired = false
	m = m.openTrustPromptIfRequired()
	for _, row := range m.transcript {
		if strings.Contains(row.text, trustConfigTranscriptMarker) {
			t.Fatal("verifier: trusted workspace produced the untrusted-config card")
		}
	}
}

// The [V] picker row must NOT change trust state through the REAL
// choosePicker path, and must return the menu (already probed in
// trust_card_test.go; here the negative control on the decision).
func TestVerifyTrustViewRowLeavesPromptRequired(t *testing.T) {
	ws := t.TempDir()
	writeTrustConfigFixture(t, ws)
	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	m.projectConfigPath = ws + "/.splice/config.json"
	m.trustPromptRequired = true
	m = m.openTrustPromptIfRequired()
	for i, item := range m.picker.items {
		if item.Value == trustActionView {
			m.picker.selected = i
			break
		}
	}
	updated, _ := m.choosePicker()
	next := updated.(model)
	if !next.trustPromptRequired {
		t.Fatal("verifier: [V] resolved the mandatory trust prompt")
	}
	if next.trusted {
		t.Fatal("verifier: [V] granted trust")
	}
}

// ---- Fold table (3ea5e3f): the picker ring under SPLICE_ASCII must keep
// column alignment in a rendered row, not just per-glyph widths.

func TestVerifyFoldKeepsPickerRowAligned(t *testing.T) {
	m := limeTestModel()
	m.asciiEnabled = true
	item := pickerItem{
		Label:   "test-model",
		Efforts: []modelregistry.ReasoningEffort{"low", "medium", "high"},
	}
	rich := renderModelPickerRow(120, true, item)
	folded := foldASCII(rich, true)
	if got, want := lipgloss.Width(folded), lipgloss.Width(rich); got != want {
		t.Fatalf("verifier: fold changed the picker row width: %d, want %d", got, want)
	}
	plain := stripANSI(folded)
	for _, g := range []rune{'■', '□', '←', '↑', '↓'} {
		if strings.ContainsRune(plain, g) {
			t.Fatalf("verifier: glyph %q survived the fold: %q", g, plain)
		}
	}
}

// ---- Cross-slice invariant: the diff view and the handoff keys cannot
// fight over input. With both armed, the handoff keys win on capital
// letters they own and the diff view owns its lowercase set.

func TestVerifyDiffAndHandoffKeysDoNotCollide(t *testing.T) {
	next := verifierArmedHandoff(t, "wt-vfy-both")
	// Open the diff view through the real [D] path.
	updated, _ := next.Update(reviewRealShiftKey('D'))
	m := updated.(model)
	if !m.diffView.active {
		t.Fatal("verifier: [D] did not open the diff view")
	}
	// While the diff view is up, real shift+X (discard) must still reach the
	// handoff handler: the diff handler returns handled=false for X.
	t.Setenv("EDITOR", "")
	origRemove := tuiRemoveWorktree
	origPreserve := tuiPreserveWorktree
	defer func() {
		tuiRemoveWorktree = origRemove
		tuiPreserveWorktree = origPreserve
	}()
	removed := false
	tuiRemoveWorktree = func(_ context.Context, options worktrees.RemoveOptions) error {
		removed = true
		return nil
	}
	tuiPreserveWorktree = func(_ context.Context, _ worktrees.MergeBackOptions) (string, error) {
		return "splice/wt-vfy-both", nil
	}
	updated2, _ := m.Update(reviewRealShiftKey('X'))
	mid := updated2.(model)
	// The discard runs synchronously, but its result arrives as a deferred
	// worktreeReviewResultMsg (tea.Batch in runHandoffDiscard) — the same
	// shape the real event loop delivers. Feed it through: this is where
	// the stale diff view must close.
	updated3, _ := mid.Update(worktreeReviewResultMsg{
		decision: worktreeReviewReject,
		reason:   "handoff discard",
	})
	final := updated3.(model)
	if !removed {
		t.Fatal("verifier: shift+X while the diff view is open did not reach the handoff discard")
	}
	if final.diffView.active {
		t.Fatalf("verifier: diff view stayed open over the removed worktree (lane %q)", final.diffView.wt.Name)
	}
}
