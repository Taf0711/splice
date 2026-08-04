package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHandleConfigCommandReportsPersistError: a failed persist surfaces an error
// instead of falsely reporting "recaps: on/off".
func TestHandleConfigCommandReportsPersistError(t *testing.T) {
	// Point the config path under a regular file, so the write can't create it.
	dir := t.TempDir()
	parent := filepath.Join(dir, "afile")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := model{userConfigPath: filepath.Join(parent, "config.json")}
	if _, text := m.handleConfigCommand("recaps off"); !strings.Contains(text, "Failed to save") {
		t.Errorf("a failed persist should report failure, got %q", text)
	}
}

// TestMaybeRecapTurnGating: a recap fires only when enabled, a provider exists,
// and the turn passes the work and answer gates, at most once per run.
func TestMaybeRecapTurnGating(t *testing.T) {
	withProvider := func() model {
		return model{recapsEnabled: true, provider: &fakeProvider{}}
	}
	qualifyingRows := recapTurnRows(strings.Repeat("a", recapMinAnswerChars), 1)

	// Disabled -> no cmd.
	if _, cmd := (model{recapsEnabled: false, provider: &fakeProvider{}}).maybeRecapTurn(1, qualifyingRows); cmd != nil {
		t.Error("recaps disabled: want no cmd")
	}
	// No provider -> no cmd.
	if _, cmd := (model{recapsEnabled: true}).maybeRecapTurn(1, qualifyingRows); cmd != nil {
		t.Error("no provider: want no cmd")
	}
	// Empty answer -> no cmd.
	if _, cmd := withProvider().maybeRecapTurn(1, recapTurnRows("   ", 1)); cmd != nil {
		t.Error("empty answer: want no cmd")
	}
	// Enabled + provider + answer -> a cmd, and the per-run gate is set.
	m, cmd := withProvider().maybeRecapTurn(7, qualifyingRows)
	if cmd == nil {
		t.Fatal("enabled qualifying run should dispatch a recap cmd")
	}
	if !m.recappedRuns[7] {
		t.Error("the per-run gate should be set")
	}
	// Second call for the same run -> no cmd (already recapped).
	if _, cmd2 := m.maybeRecapTurn(7, qualifyingRows); cmd2 != nil {
		t.Error("a second recap for the same run must not fire")
	}
}

// TestMaybeRecapTurnLongAnswerWithToolCallRecaps ensures the gate must not
// suppress the case recaps exist for: real work with a long final answer.
func TestMaybeRecapTurnLongAnswerWithToolCallRecaps(t *testing.T) {
	rows := recapTurnRows(strings.Repeat("a", recapMinAnswerChars), 1)
	if _, cmd := (model{recapsEnabled: true, provider: &fakeProvider{}}).maybeRecapTurn(1, rows); cmd == nil {
		t.Error("a long answer after a tool call should dispatch a recap cmd")
	}
}

func TestMaybeRecapTurnShortAnswerWithToolCallDoesNotRecap(t *testing.T) {
	rows := recapTurnRows(strings.Repeat("a", recapMinAnswerChars-1), 1)
	if _, cmd := (model{recapsEnabled: true, provider: &fakeProvider{}}).maybeRecapTurn(1, rows); cmd != nil {
		t.Error("a short answer after a tool call must not dispatch a recap cmd")
	}
}

func TestMaybeRecapTurnLongAnswerWithoutToolCallDoesNotRecap(t *testing.T) {
	rows := recapTurnRows(strings.Repeat("a", recapMinAnswerChars), 0)
	if _, cmd := (model{recapsEnabled: true, provider: &fakeProvider{}}).maybeRecapTurn(1, rows); cmd != nil {
		t.Error("a long answer without a tool call must not dispatch a recap cmd")
	}
}

func TestMaybeRecapTurnFailsBothGatesDoesNotRecap(t *testing.T) {
	rows := recapTurnRows(strings.Repeat("a", recapMinAnswerChars-1), 0)
	if _, cmd := (model{recapsEnabled: true, provider: &fakeProvider{}}).maybeRecapTurn(1, rows); cmd != nil {
		t.Error("a short answer without a tool call must not dispatch a recap cmd")
	}
}

func TestMaybeRecapTurnPerRunGuardPreventsDoubleFire(t *testing.T) {
	rows := recapTurnRows(strings.Repeat("a", recapMinAnswerChars), 1)
	m, first := (model{recapsEnabled: true, provider: &fakeProvider{}}).maybeRecapTurn(1, rows)
	if first == nil {
		t.Fatal("a qualifying turn should dispatch a recap cmd")
	}
	if _, second := m.maybeRecapTurn(1, rows); second != nil {
		t.Error("the per-run guard must prevent a second recap cmd")
	}
}

func recapTurnRows(answer string, toolCalls int) []transcriptRow {
	rows := make([]transcriptRow, 0, toolCalls+1)
	for i := 0; i < toolCalls; i++ {
		rows = append(rows, transcriptRow{kind: rowToolCall, id: "call"})
	}
	return append(rows, transcriptRow{kind: rowAssistant, final: true, text: answer})
}

// TestHandleRecapGenerated: a successful recap appends a "※ recap:" row; a
// failed/empty one appends nothing and releases the gate.
func TestHandleRecapGenerated(t *testing.T) {
	// runID matches the latest run -> the recap appends.
	m := model{runID: 5, recappedRuns: map[int]bool{5: true}}
	m, _ = m.handleRecapGenerated(recapGeneratedMsg{runID: 5, recap: "Built a fashion site with cart and blog"})
	if len(m.transcript) != 1 {
		t.Fatalf("a recap for the current run should append one row, got %d", len(m.transcript))
	}
	last := m.transcript[len(m.transcript)-1]
	if last.kind != rowRecap || !strings.Contains(last.text, "fashion site") {
		t.Errorf("recap row wrong: %+v", last)
	}
	if got := plainRender(t, renderRecapRow(last, 100)); !strings.Contains(got, "※ recap:") {
		t.Errorf("recap render should show the ※ marker, got %q", got)
	}

	// A recap from a STALE run (a newer turn started) is dropped, not appended to
	// the wrong conversation; the gate is released.
	stale := model{runID: 8, recappedRuns: map[int]bool{6: true}}
	stale, _ = stale.handleRecapGenerated(recapGeneratedMsg{runID: 6, recap: "old run recap"})
	if len(stale.transcript) != 0 {
		t.Error("a stale-run recap must not be appended")
	}
	if stale.recappedRuns[6] {
		t.Error("a stale-run recap must release the per-run gate")
	}

	// Failed recap: no row, gate released.
	m2 := model{runID: 9, recappedRuns: map[int]bool{9: true}}
	m2, _ = m2.handleRecapGenerated(recapGeneratedMsg{runID: 9, err: errExampleRecap})
	if len(m2.transcript) != 0 {
		t.Error("a failed recap must append nothing")
	}
	if m2.recappedRuns[9] {
		t.Error("a failed recap must release the per-run gate")
	}
}

// TestHandleConfigCommandTogglesRecaps: /config recaps on|off flips the flag and
// reports it; unknown args are rejected.
func TestHandleConfigCommandTogglesRecaps(t *testing.T) {
	m := model{recapsEnabled: true}
	m, text := m.handleConfigCommand("recaps off")
	if m.recapsEnabled || !strings.Contains(text, "recaps: off") {
		t.Errorf("recaps off: enabled=%v text=%q", m.recapsEnabled, text)
	}
	m, text = m.handleConfigCommand("recaps on")
	if !m.recapsEnabled || !strings.Contains(text, "recaps: on") {
		t.Errorf("recaps on: enabled=%v text=%q", m.recapsEnabled, text)
	}
	if _, text := m.handleConfigCommand("bogus"); !strings.Contains(text, "Unknown setting") {
		t.Errorf("unknown arg should be rejected, got %q", text)
	}
}

var errExampleRecap = errExample("recap failed")

type errExample string

func (e errExample) Error() string { return string(e) }
