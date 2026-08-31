package tui

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/worktrees"
)

func handoffFixture(preserved bool) handoffState {
	return handoffState{
		lane:      "wt-a1",
		path:      "/repo/.splice/wt/a1",
		branch:    "splice/wt-a1",
		outcome:   "completed",
		staged:    3,
		preserved: preserved,
	}
}

// TestHandoffCardPreservedRendersResume pins P5 F2 (DoD 28): a preserved
// lane's handoff names the worktree, the branch, the resume path, the
// outcome, and the action row.
func TestHandoffCardPreservedRendersResume(t *testing.T) {
	plain := stripANSI(renderHandoffCard(handoffFixture(true), true, 90))
	for _, want := range []string{
		"HANDOFF", "wt-a1", "work alive",
		"worktree kept: /repo/.splice/wt/a1",
		"branch:        splice/wt-a1",
		"resume -> /resume wt-a1",
		"outcome: completed",
		"[O] open worktree", "[M] merge back now", "[X] discard lane",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("handoff card missing %q:\n%s", want, plain)
		}
	}
}

// TestHandoffCancelledShowsStagedNotApplied pins the receipts continuity:
// a cancelled lane's handoff carries the staged-not-applied accounting and
// the apply-from-worktree note.
func TestHandoffCancelledShowsStagedNotApplied(t *testing.T) {
	h := handoffFixture(true)
	h.outcome = "cancelled"
	plain := stripANSI(renderHandoffCard(h, true, 90))
	if !strings.Contains(plain, "cancelled") || !strings.Contains(plain, "3 file(s) staged, not applied") {
		t.Fatalf("cancelled handoff missing staged accounting:\n%s", plain)
	}
	if !strings.Contains(plain, "staged work can be applied from the worktree") {
		t.Fatalf("cancelled handoff missing the apply note:\n%s", plain)
	}
	if strings.Contains(plain, "failed") {
		t.Fatalf("cancelled handoff leaked failure language:\n%s", plain)
	}
}

// TestHandoffWorkLost pins §6.8's lane-death vs work-loss distinction: a
// missing worktree renders the WORK LOST form with the git recovery hint
// and no resume/action row.
func TestHandoffWorkLost(t *testing.T) {
	plain := stripANSI(renderHandoffCard(handoffFixture(false), false, 90))
	for _, want := range []string{
		"WORK LOST", "no longer exists",
		"git branch --list splice/wt-a1",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("work-lost handoff missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "resume ->") {
		t.Fatalf("work-lost handoff still offers resume:\n%s", plain)
	}
}

// TestHandoffMergeUnavailableOnDirty pins the legality rule: when the main
// checkout is dirty, [M] renders as a muted "merge unavailable" note, not
// a key the user can press into a broken merge.
func TestHandoffMergeUnavailableOnDirty(t *testing.T) {
	plain := stripANSI(renderHandoffCard(handoffFixture(true), false, 90))
	if !strings.Contains(plain, "[M] merge unavailable: main checkout dirty") {
		t.Fatalf("dirty-main note missing:\n%s", plain)
	}
	if strings.Contains(plain, "[M] merge back now") {
		t.Fatalf("dirty main still offered merge-back key:\n%s", plain)
	}
}

// TestHandoffPayloadRoundTrip pins the tagged-row contract: encode/decode
// reproduces the handoff data (lane, path, branch, outcome, counts,
// preserved, mergeAvailable) exactly.
func TestHandoffPayloadRoundTrip(t *testing.T) {
	original := handoffFixture(true)
	payload := handoffTranscriptPayload(original, true)
	decoded, merge, ok := parseHandoffTranscriptPayload(payload)
	if !ok {
		t.Fatal("payload failed to round-trip")
	}
	if decoded.lane != original.lane || decoded.path != original.path ||
		decoded.branch != original.branch || decoded.outcome != original.outcome ||
		decoded.staged != original.staged || decoded.preserved != original.preserved {
		t.Fatalf("round-trip mismatch: %+v vs %+v", decoded, original)
	}
	if !merge {
		t.Fatal("mergeAvailable lost in round-trip")
	}
}

// TestOfferHandoffAppendsCard proves the runtime wiring: a model with an
// existing worktree path appends the handoff payload row to the transcript;
// a nil or empty worktree appends nothing.
func TestOfferHandoffAppendsCard(t *testing.T) {
	m := mouseTestModel()
	before := len(m.transcript)
	// A path that does not exist on disk: the work-lost form still renders
	// (lane death vs work loss), so the row count still grows.
	m.offerHandoff(&worktrees.Result{Name: "wt-x", Path: "/nonexistent/wt-x"}, "failed", 0, 0)
	if len(m.transcript) != before+1 {
		t.Fatalf("offerHandoff did not append a row")
	}
	if !strings.Contains(m.transcript[len(m.transcript)-1].text, handoffTranscriptMarker) {
		t.Fatalf("appended row is not a handoff payload: %q", m.transcript[len(m.transcript)-1].text)
	}
	// Nil worktree: no row.
	m.offerHandoff(nil, "failed", 0, 0)
	if len(m.transcript) != before+1 {
		t.Fatalf("nil worktree appended a row")
	}
}

// TestErrorRowRendersHandoffCard proves the render path is WIRED: a system
// row carrying a handoff payload renders the card, and the marker never
// leaks.
func TestErrorRowRendersHandoffCard(t *testing.T) {
	m := mouseTestModel()
	row := transcriptRow{
		kind: rowSystem,
		text: handoffTranscriptPayload(handoffFixture(true), true),
	}
	plain := stripANSI(m.renderRowModeUncached(row, 100, rowContext{}, cardRenderOptions{}))
	if !strings.Contains(plain, "HANDOFF") || !strings.Contains(plain, "resume -> /resume wt-a1") {
		t.Fatalf("handoff payload did not render as a card:\n%s", plain)
	}
	if strings.Contains(plain, "\x00") {
		t.Fatalf("handoff marker leaked into the render:\n%s", plain)
	}
}
