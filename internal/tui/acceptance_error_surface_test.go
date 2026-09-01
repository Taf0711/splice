package tui

// acceptance_error_surface_test.go (P1 verifier expansion): error surfaces
// probed end to end. Discipline from the review passes: real message shapes
// through Update, the live View() as render truth (not bare renderer calls),
// and negative controls where a surface must NOT appear. The provider-failure
// wire (errhint) renders a next step under recognized failures and nothing
// under unknown ones; partial streamed output survives the failure; the run
// releases; a failed diff capture renders its honest error through the real
// message path; stale error results never poison a live lane.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/errhint"
	"github.com/Taf0711/splice/internal/worktrees"
)

// A provider auth failure arriving on agentResponseMsg must:
// append an error row carrying the classification hint, keep the streamed
// partial answer, release the run (pending false, activeRunID 0), and show
// the hint in the live View.
func TestVerifyAgentErrorRowHintAndRelease(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 33
	err := errors.New("auth error: your API key is missing or invalid")
	updated, _ := m.Update(agentResponseMsg{
		runID: 33,
		rows:  []transcriptRow{{kind: rowAssistant, text: "partial answer before the drop", final: false}},
		err:   err,
	})
	next := updated.(model)

	if next.pending {
		t.Fatal("verifier: failed run left the model pending")
	}
	if next.activeRunID != 0 {
		t.Fatal("verifier: failed run did not release activeRunID")
	}

	var errRow *transcriptRow
	partial := false
	for i, row := range next.transcript {
		if row.kind == rowAssistant && strings.Contains(row.text, "partial answer") {
			partial = true
		}
		if row.kind == rowError && strings.Contains(row.text, "API key") {
			r := next.transcript[i]
			errRow = &r
		}
	}
	if !partial {
		t.Fatal("verifier: partial streamed answer vanished on failure")
	}
	if errRow == nil {
		t.Fatal("verifier: no error row appended for the failed turn")
	}
	wantHint := errhint.TUIHint(err)
	if errRow.hint != wantHint || wantHint == "" {
		t.Fatalf("verifier: error row hint = %q, want classified auth hint", errRow.hint)
	}
	if !errRow.final {
		t.Fatal("verifier: error row is not final (turn should terminate on it)")
	}
	// Render truth: the hint is visible in the live frame.
	next.pending = false
	plain := plainRender(t, next.View())
	if !strings.Contains(plain, "API key") {
		t.Fatal("verifier: error text not visible in live View")
	}
	if !strings.Contains(plain, "/provider") {
		t.Fatal("verifier: classified hint not visible in live View")
	}
}

// Negative control: an UNKNOWN provider failure must render the raw error
// but no invented next step (errhint returns "" and the row must carry that).
func TestVerifyUnknownErrorCarriesNoHint(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 34
	err := errors.New("provider error: mystery failure")
	updated, _ := m.Update(agentResponseMsg{runID: 34, err: err})
	next := updated.(model)
	for _, row := range next.transcript {
		if row.kind == rowError && strings.Contains(row.text, "mystery failure") {
			if row.hint != "" {
				t.Fatalf("verifier: unknown failure invented a hint: %q", row.hint)
			}
			return
		}
	}
	t.Fatal("verifier: unknown-failure error row missing")
}

// A LOCAL failure (no provider marker) must never draw a provider hint even
// though its text contains a keyword the classifier would otherwise match.
func TestVerifyLocalErrorNeverDrawsProviderHint(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 35
	// "permission denied" is an Auth signature, but without a provider
	// marker prefix it is a local failure.
	err := errors.New("permission denied writing /usr/local/bin/splice")
	if got := errhint.TUIHint(err); got != "" {
		t.Fatalf("verifier: errhint attached a provider hint to a local error: %q", got)
	}
	updated, _ := m.Update(agentResponseMsg{runID: 35, err: err})
	next := updated.(model)
	for _, row := range next.transcript {
		if row.kind == rowError && strings.Contains(row.text, "permission denied") {
			if row.hint != "" {
				t.Fatalf("verifier: local error row carried provider hint %q", row.hint)
			}
			return
		}
	}
	t.Fatal("verifier: local-failure error row missing")
}

// The diff view's failure path through the REAL message flow: a capture
// error lands in diffView.err, the pane renders the honest error + recovery
// line in the live View, and a later good capture REPLACES the error.
func TestVerifyDiffCaptureFailureRecovers(t *testing.T) {
	m := mouseTestModel()
	opened, _ := m.openDiffReview(worktrees.Result{Name: "wt-err-diff", Path: "/tmp/wt-err-diff", RepoRoot: "/tmp"})
	if !opened.diffView.active {
		t.Fatal("verifier: diff view did not open")
	}
	// Failure lands first (real capture cmds return err on the msg).
	failed := opened.handleDiffCaptured(diffCapturedMsg{
		lane: "wt-err-diff",
		err:  errors.New("exit status 128: fatal: not a git repository"),
	})
	if failed.diffView.err == "" {
		t.Fatal("verifier: capture failure did not set the error state")
	}
	if failed.diffView.text != "" || failed.diffView.files != nil {
		t.Fatal("verifier: failed capture left stale diff content")
	}
	plain := plainRender(t, failed.View())
	for _, want := range []string{"Could not produce the diff", "exit status 128", "Recovery: git"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("verifier: diff error pane missing %q in live View", want)
		}
	}
	// Recovery: a good capture for the same lane replaces the error.
	recovered := failed.handleDiffCaptured(diffCapturedMsg{
		lane: "wt-err-diff",
		res:  diffReviewTestDiff,
	})
	if recovered.diffView.err != "" {
		t.Fatal("verifier: good capture did not clear the error state")
	}
	plainOK := plainRender(t, recovered.View())
	if strings.Contains(plainOK, "Could not produce the diff") {
		t.Fatal("verifier: error pane survived a successful recapture")
	}
	if !strings.Contains(plainOK, "retry.go") {
		t.Fatal("verifier: recovered diff content missing from the live View")
	}
}

// Stale-capture identity: an error result for a lane that is no longer on
// screen must be dropped (stale-async discipline, §7).
func TestVerifyStaleDiffErrorDropped(t *testing.T) {
	m := mouseTestModel()
	opened, _ := m.openDiffReview(worktrees.Result{Name: "wt-live", Path: "/tmp/wt-live", RepoRoot: "/tmp"})
	stale := opened.handleDiffCaptured(diffCapturedMsg{
		lane: "wt-old-lane",
		err:  errors.New("late failure from a closed lane"),
	})
	if stale.diffView.err != "" {
		t.Fatal("verifier: stale error poisoned the live diff view")
	}
	if stale.diffView.text != "" {
		t.Fatal("verifier: stale capture carried content into the live view")
	}
}

// The CANCELLED receipt for context cancellation must stay distinct from
// FAILED at the render level (contract: cancelled != failed), through the
// real Update path for both outcomes on the same failure kind.
func TestVerifyCancelledAndFailedStayDistinct(t *testing.T) {
	m := mouseTestModel()
	m.activeRunID = 51
	cancelled, _ := m.Update(planExecutionResultMsg{runID: 51, err: context.Canceled})
	nextC := cancelled.(model)
	failedM := mouseTestModel()
	failedM.activeRunID = 52
	failed, _ := failedM.Update(planExecutionResultMsg{runID: 52, err: acceptErr("verification gate rejected the build")})
	nextF := failed.(model)

	collect := func(rows []transcriptRow) map[string]string {
		out := map[string]string{}
		for _, row := range rows {
			if card, ok := parseReceiptTranscriptPayload(row.text); ok {
				out[string(card.kind)] = stripANSI(renderReceiptCard(card, 90))
			}
		}
		return out
	}
	cards := collect(nextC.transcript)
	if _, ok := cards[string(receiptCancelled)]; !ok {
		t.Fatalf("verifier: cancelled receipt missing: %v", keysOf(cards))
	}
	fcards := collect(nextF.transcript)
	if _, ok := fcards[string(receiptFailed)]; !ok {
		t.Fatalf("verifier: failed receipt missing: %v", keysOf(fcards))
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
