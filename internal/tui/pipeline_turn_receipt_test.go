package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/worktrees"
)

// pipelineResultJSON is the FinalAnswer a pipeline run returns (PipelineResult).
func pipelineResultJSON(t *testing.T, result schemas.PipelineResult) string {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func flatCardText(card receiptCard) string {
	return strings.Join(strings.Fields(strings.Join(card.lines, " ")), " ")
}

func TestPipelineTurnReceiptVerifiedCard(t *testing.T) {
	answer := pipelineResultJSON(t, schemas.PipelineResult{RunID: "r1", Status: "completed", Verified: true})
	card, ok := pipelineTurnReceipt(answer, "", nil, nil, time.Time{}, time.Time{})
	if !ok || card.kind != receiptVerified || card.title != "VERIFIED" {
		t.Fatalf("verified run: ok=%v kind=%q title=%q, want VERIFIED", ok, card.kind, card.title)
	}
	// A result with no check detail says so instead of an empty body.
	if text := flatCardText(card); !strings.Contains(text, "records no check detail") {
		t.Fatalf("verified card without evidence = %q, want the explicit no-detail line", text)
	}
}

// A VERIFIED pipeline card shows the evidence the pipeline result records.
func TestPipelineTurnReceiptVerifiedCardShowsPipelineEvidence(t *testing.T) {
	answer := pipelineResultJSON(t, schemas.PipelineResult{
		RunID:      "r1e",
		Status:     "completed",
		Verified:   true,
		FinalState: &schemas.IterationState{TestsPassing: 4, AcceptanceFactsPassing: 2},
		Stages:     []schemas.StageRecord{{Name: "test_runner", Status: schemas.StageCompleted, TestCommand: "go test ./..."}},
	})
	card, _ := pipelineTurnReceipt(answer, "", nil, nil, time.Time{}, time.Time{})
	text := flatCardText(card)
	for _, want := range []string{"[+] 4 test(s) passed", "[+] 2 acceptance fact(s) passed", "[+] test command: go test ./..."} {
		if !strings.Contains(text, want) {
			t.Fatalf("verified card = %q, want %q", text, want)
		}
	}
	if strings.Contains(text, "no check detail") {
		t.Fatalf("verified card with evidence also shows the no-detail line: %q", text)
	}
}

// The demo replay returns a pipeline result, so a demo success shows its
// VERIFIED card, not FAILED "could not be read".
func TestTUIDemoReplayResultReadsAsVerifiedReceipt(t *testing.T) {
	origPause := demoStepPause
	demoStepPause = 0
	t.Cleanup(func() { demoStepPause = origPause })
	result, err := replayWorktreeRejectDemo(context.Background(), agent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	card, ok := pipelineTurnReceipt(result.FinalAnswer, result.IncompleteReason, nil, nil, time.Time{}, time.Time{})
	if !ok || card.kind != receiptVerified {
		t.Fatalf("demo receipt kind = %q (%q), want VERIFIED", card.kind, flatCardText(card))
	}
}

func TestPipelineTurnReceiptNotVerifiedCarriesReason(t *testing.T) {
	const reason = "no check verified the change"
	answer := pipelineResultJSON(t, schemas.PipelineResult{RunID: "r2", Status: "completed", Verified: false, UnverifiedReason: reason})
	card, ok := pipelineTurnReceipt(answer, "", nil, nil, time.Time{}, time.Time{})
	if !ok || card.kind != receiptUnverified || card.title != "DONE · NOT VERIFIED" {
		t.Fatalf("unverified run: ok=%v kind=%q title=%q, want DONE · NOT VERIFIED", ok, card.kind, card.title)
	}
	if !strings.Contains(flatCardText(card), reason) {
		t.Fatalf("unverified card lost its reason: %q", flatCardText(card))
	}
}

func TestPipelineTurnReceiptFailedCarriesAbortReason(t *testing.T) {
	abort := "stage code_writer exhausted its budget"
	answer := pipelineResultJSON(t, schemas.PipelineResult{RunID: "r3", Status: "failed", AbortReason: &abort})
	card, ok := pipelineTurnReceipt(answer, "", nil, nil, time.Time{}, time.Time{})
	if !ok || card.kind != receiptFailed || card.title != "FAILED" {
		t.Fatalf("failed run: ok=%v kind=%q, want FAILED", ok, card.kind)
	}
	if !strings.Contains(flatCardText(card), abort) {
		t.Fatalf("failed card lost its abort reason: %q", flatCardText(card))
	}
}

// A run error keeps its full text in the FAILED card, so the cause stays visible.
func TestPipelineTurnReceiptRunErrorShowsFailedWithReason(t *testing.T) {
	const cause = "provider stream broke at chunk 42 with a long wrapped detail"
	card, ok := pipelineTurnReceipt("", "", errors.New(cause), nil, time.Time{}, time.Time{})
	if !ok || card.kind != receiptFailed {
		t.Fatalf("run error: ok=%v kind=%q, want FAILED", ok, card.kind)
	}
	for _, width := range []int{80, 120} {
		out := stripANSI(renderReceiptCard(card, width, "ascii"))
		if !strings.Contains(strings.Join(strings.Fields(out), " "), "chunk 42") {
			t.Fatalf("width %d: FAILED card cut off the cause:\n%s", width, out)
		}
	}
}

func TestPipelineTurnReceiptCancelIsCancelledNotFailed(t *testing.T) {
	card, ok := pipelineTurnReceipt("", "", fmt.Errorf("pipeline: %w", context.Canceled), nil, time.Time{}, time.Time{})
	if !ok || card.kind != receiptCancelled {
		t.Fatalf("cancel: ok=%v kind=%q, want CANCELLED", ok, card.kind)
	}
}

// Every status the orchestrator writes maps like finishPresentation in
// run.go: only a typed user abort is CANCELLED, every other non-completed
// status is FAILED and keeps its abort reason.
func TestPipelineTurnReceiptMapsEveryRunStatus(t *testing.T) {
	reason := func(s string) *string { return &s }
	cases := []struct {
		name       string
		result     schemas.PipelineResult
		wantKind   receiptKind
		wantReason string
	}{
		{"user abort", schemas.PipelineResult{Status: "aborted", UserAborted: true, AbortReason: reason("user_abort: stop")}, receiptCancelled, "stopped by you"},
		{"max iterations", schemas.PipelineResult{Status: "aborted", AbortReason: reason("reached max iterations (3) without success")}, receiptFailed, "reached max iterations (3) without success"},
		{"wall time", schemas.PipelineResult{Status: "aborted", AbortReason: reason("wall time exceeded")}, receiptFailed, "wall time exceeded"},
		{"trajectory abort", schemas.PipelineResult{Status: "aborted", AbortReason: reason("abort: no progress")}, receiptFailed, "abort: no progress"},
		{"rollback refuse", schemas.PipelineResult{Status: "aborted", AbortReason: reason("rollback requires an isolated --worktree: x")}, receiptFailed, "rollback requires an isolated --worktree"},
		{"failed", schemas.PipelineResult{Status: "failed", AbortReason: reason("stage code_writer failed")}, receiptFailed, "stage code_writer failed"},
		{"aborted without reason", schemas.PipelineResult{Status: "aborted"}, receiptFailed, `status "aborted"`},
		{"unknown status", schemas.PipelineResult{Status: "cancelled"}, receiptFailed, `status "cancelled"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card, ok := pipelineTurnReceipt(pipelineResultJSON(t, tc.result), "", nil, nil, time.Time{}, time.Time{})
			if !ok || card.kind != tc.wantKind {
				t.Fatalf("ok=%v kind=%q, want %q", ok, card.kind, tc.wantKind)
			}
			if text := flatCardText(card); !strings.Contains(text, tc.wantReason) || strings.Contains(text, "unknown status") {
				t.Fatalf("card text %q, want %q", text, tc.wantReason)
			}
		})
	}
}

// The live turn handler shows the verified card and arms its keys.
func TestPipelineTurnSuccessHandlerShowsReceiptRow(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 41
	answer := pipelineResultJSON(t, schemas.PipelineResult{RunID: "r5", Status: "completed", Verified: true})
	card, _ := pipelineTurnReceipt(answer, "", nil, nil, time.Time{}, time.Time{})
	payload := receiptTranscriptPayload(card)
	updated, _ := m.Update(agentResponseMsg{runID: 41, rows: []transcriptRow{{kind: rowAssistant, text: "done", final: true}, {kind: rowError, text: payload}}, receipt: payload})
	next := updated.(model)
	if next.lastTerminalReceipt != receiptVerified {
		t.Fatalf("lastTerminalReceipt = %q, want verified", next.lastTerminalReceipt)
	}
	if !transcriptHasReceipt(next, "VERIFIED") {
		t.Fatal("verified /exec turn did not show a VERIFIED card")
	}
}

// A pipeline run error shows the FAILED card in place of the raw error row.
func TestPipelineTurnErrorHandlerShowsFailedCard(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 42
	err := errors.New("pipeline run broke: tool registry refused the stage")
	card, _ := pipelineTurnReceipt("", "", err, nil, time.Time{}, time.Time{})
	payload := receiptTranscriptPayload(card)
	updated, _ := m.Update(agentResponseMsg{runID: 42, err: err, receipt: payload})
	next := updated.(model)
	if next.lastTerminalReceipt != receiptFailed {
		t.Fatalf("lastTerminalReceipt = %q, want failed", next.lastTerminalReceipt)
	}
	if !transcriptHasReceipt(next, "FAILED") {
		t.Fatal("pipeline error did not show a FAILED card")
	}
	for _, row := range next.transcript {
		if row.kind == rowError && row.text == err.Error() {
			t.Fatal("pipeline error also rendered the raw error row")
		}
	}
}

// A recognized provider failure keeps its next-step hint and the turn
// metadata under the FAILED card that replaces the raw error row.
func TestPipelineTurnFailedCardKeepsHintAndTurnMetadata(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 45
	err := errors.New("code_writer: stream error: provider request error: 401 Unauthorized: invalid api key")
	card, _ := pipelineTurnReceipt("", "", err, nil, time.Time{}, time.Time{})
	payload := receiptTranscriptPayload(card)
	updated, _ := m.Update(agentResponseMsg{runID: 45, err: err, receipt: payload, turnTools: 3, turnElapsed: 75 * time.Second})
	next := updated.(model)
	var row transcriptRow
	found := false
	for _, r := range next.transcript {
		if r.text == payload {
			row, found = r, true
		}
	}
	if !found {
		t.Fatal("pipeline error did not show the FAILED card row")
	}
	if !strings.Contains(row.hint, "/provider") || !row.final || row.turnTools != 3 || row.turnElapsed != 75*time.Second {
		t.Fatalf("card row lost hint or turn metadata: hint=%q final=%v tools=%d elapsed=%s", row.hint, row.final, row.turnTools, row.turnElapsed)
	}
	out := stripANSI(next.renderRow(row, 100, rowContext{}))
	if !strings.Contains(out, "FAILED") || !strings.Contains(out, "→ API key rejected") {
		t.Fatalf("FAILED card does not render the hint line below it:\n%s", out)
	}
	if strings.Index(out, "FAILED") > strings.Index(out, "→ API key rejected") {
		t.Fatalf("hint line renders above the card:\n%s", out)
	}
}

// A cancelled pipeline turn shows CANCELLED from its flush path.
func TestPipelineTurnCancelFlushShowsCancelledCard(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 0
	m.flushRunIDs = map[int]string{43: ""}
	card, _ := pipelineTurnReceipt("", "", context.Canceled, nil, time.Time{}, time.Time{})
	payload := receiptTranscriptPayload(card)
	updated, _ := m.Update(agentResponseMsg{runID: 43, err: context.Canceled, receipt: payload})
	next := updated.(model)
	if next.lastTerminalReceipt != receiptCancelled {
		t.Fatalf("lastTerminalReceipt = %q, want cancelled", next.lastTerminalReceipt)
	}
	if !transcriptHasReceipt(next, "CANCELLED") {
		t.Fatal("cancelled pipeline turn did not show a CANCELLED card")
	}
}

// A cancelled pipeline turn with a worktree adopts its lane in the session it
// ran in: the card's [A]/[D] keys act on that worktree, which stays locked.
// After /resume the run's session is not the active one: no card lands in
// this transcript, no lane key is armed, and the worktree is released.
func TestPipelineTurnCancelFlushAdoptsLaneOnlyInItsSession(t *testing.T) {
	origUnlock := tuiUnlockWorktree
	t.Cleanup(func() { tuiUnlockWorktree = origUnlock })
	for _, tc := range []struct {
		name          string
		flushSession  string
		wantCard      bool
		wantUnlocked  bool
		wantWorktree  bool
		staleWorktree *worktrees.Result
	}{
		{name: "same session", flushSession: "s1", wantCard: true, wantWorktree: true},
		{name: "after resume", flushSession: "s-old", wantUnlocked: true, staleWorktree: &worktrees.Result{Name: "other", Path: "/other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unlocked := false
			tuiUnlockWorktree = func(context.Context, worktrees.UnlockOptions) error {
				unlocked = true
				return nil
			}
			m := mouseTestModel()
			m.activeSession.SessionID = "s1"
			m.activeWorktree = tc.staleWorktree
			m.flushRunIDs = map[int]string{46: tc.flushSession}
			wt := &worktrees.Result{Name: "tui-46", Path: t.TempDir(), RepoRoot: t.TempDir(), Locked: true}
			card, _ := pipelineTurnReceipt("", "", context.Canceled, wt, time.Time{}, time.Time{})
			updated, _ := m.Update(agentResponseMsg{runID: 46, err: context.Canceled, receipt: receiptTranscriptPayload(card), worktree: wt})
			next := updated.(model)
			if got := transcriptHasReceipt(next, "CANCELLED"); got != tc.wantCard {
				t.Fatalf("CANCELLED card in transcript = %v, want %v", got, tc.wantCard)
			}
			if unlocked != tc.wantUnlocked {
				t.Fatalf("worktree unlocked = %v, want %v", unlocked, tc.wantUnlocked)
			}
			if tc.wantWorktree {
				if next.activeWorktree != wt || next.lastTerminalReceipt != receiptCancelled {
					t.Fatalf("lane not adopted: activeWorktree=%v receipt=%q", next.activeWorktree, next.lastTerminalReceipt)
				}
				return
			}
			if next.lastTerminalReceipt != "" {
				t.Fatalf("lastTerminalReceipt = %q, want no armed keys in the resumed session", next.lastTerminalReceipt)
			}
			if next.activeWorktree != tc.staleWorktree {
				t.Fatalf("activeWorktree changed to %v in the resumed session", next.activeWorktree)
			}
		})
	}
}

// A plain chat turn projects no receipt card.
func TestPlainChatTurnHasNoReceiptCard(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 44
	updated, _ := m.Update(agentResponseMsg{runID: 44, rows: []transcriptRow{{kind: rowAssistant, text: "hello", final: true}}})
	next := updated.(model)
	if transcriptHasAnyReceipt(next) {
		t.Fatal("plain chat turn projected a receipt card")
	}
	off := flags.Set{flags.TUIPipelineEnabled: false}
	if usesPipeline(tuiRunPipeline, off, false) {
		t.Fatal("plain chat must not run the pipeline when the flag is off")
	}
	for _, kind := range []tuiRunKind{tuiRunSpecDraft, tuiRunDesignConversation} {
		if usesPipeline(kind, off, true) {
			t.Fatalf("run kind %v must not project pipeline receipts", kind)
		}
	}
}

// Persisted receipts replay on resume: a verified turn, an error, and a cancel.
func TestPipelineReceiptReplaysOnResume(t *testing.T) {
	answer := pipelineResultJSON(t, schemas.PipelineResult{RunID: "r6", Status: "completed", Verified: true})
	card, _ := pipelineTurnReceipt(answer, "", nil, nil, time.Time{}, time.Time{})
	payload := receiptTranscriptPayload(card)
	assistant, _ := json.Marshal(map[string]any{"role": "assistant", "content": answer, "receipt": payload})
	errCard, _ := pipelineTurnReceipt("", "", errors.New("boom"), nil, time.Time{}, time.Time{})
	errRaw, _ := json.Marshal(map[string]any{"receipt": receiptTranscriptPayload(errCard)})
	cancelCard, _ := pipelineTurnReceipt("", "", context.Canceled, nil, time.Time{}, time.Time{})
	cancelRaw, _ := json.Marshal(map[string]any{"role": "receipt", "content": receiptTranscriptPayload(cancelCard)})
	events := []sessions.Event{
		{Type: sessions.EventMessage, Payload: assistant},
		{Type: sessions.EventError, Payload: errRaw},
		{Type: sessions.EventMessage, Payload: cancelRaw},
	}
	rows := transcriptRowsFromSessionEvents(events)
	var kinds []receiptKind
	for _, row := range rows {
		if card, ok := parseReceiptTranscriptPayload(row.text); ok {
			kinds = append(kinds, card.kind)
		}
	}
	if len(kinds) != 3 || kinds[0] != receiptVerified || kinds[1] != receiptFailed || kinds[2] != receiptCancelled {
		t.Fatalf("replayed receipt kinds = %v, want [verified failed cancelled]", kinds)
	}
}

func transcriptHasReceipt(m model, title string) bool {
	for _, row := range m.transcript {
		if card, ok := parseReceiptTranscriptPayload(row.text); ok && card.title == title {
			return true
		}
	}
	return false
}

func transcriptHasAnyReceipt(m model) bool {
	for _, row := range m.transcript {
		if _, ok := parseReceiptTranscriptPayload(row.text); ok {
			return true
		}
	}
	return false
}
