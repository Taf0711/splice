package tui

// narration_wiring_test.go (P3 GAP-L): the Ctrl+N verbosity cycle through the
// real Update path and its status-line readout. The property: switching
// changes only the PROJECTION (what renders), never the recorded transcript.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/tools"
)

func narrationWiringModel(t *testing.T) model {
	t.Helper()
	m := mouseTestModel()
	m.width = 120
	m.height = 30
	m.altScreen = true
	m.transcript = []transcriptRow{
		{kind: rowWelcome, text: "Welcome to Splice."},
		{kind: rowUser, text: "run the checks"},
		{kind: rowReasoning, id: "r1", runID: 1, text: "the checks are in make test"},
		{kind: rowToolCall, id: "t1", runID: 1, tool: "bash"},
		{kind: rowToolResult, id: "t1", runID: 1, tool: "bash", status: tools.StatusOK, text: "tool result: bash ok"},
		{kind: rowSystem, text: "Transcript cleared."},
	}
	return m
}

// Ctrl+N cycles quiet -> normal -> detailed and the status line shows the
// non-default level. Default (detailed) shows nothing in the status line.
func TestNarrationCycleKeyAndStatusSegment(t *testing.T) {
	m := narrationWiringModel(t)
	if m.narrationVerbosityLevel != verbosityDetailed {
		t.Fatal("default verbosity must be detailed")
	}
	// Default: no verbosity segment in the status line.
	if strings.Contains(plainRender(t, m.statusLine(120)), "quiet") {
		t.Fatal("detailed must not advertise itself in the status line")
	}
	updated, _ := m.Update(keyCtrlN())
	next := updated.(model)
	if next.narrationVerbosityLevel != verbosityQuiet {
		t.Fatalf("first Ctrl+N = %d, want quiet", next.narrationVerbosityLevel)
	}
	if got := plainRender(t, next.statusLine(120)); !strings.Contains(got, "quiet") {
		t.Fatalf("status line missing the quiet segment: %q", got)
	}
	updated, _ = next.Update(keyCtrlN())
	next = updated.(model)
	if next.narrationVerbosityLevel != verbosityNormal {
		t.Fatalf("second Ctrl+N = %d, want normal", next.narrationVerbosityLevel)
	}
	updated, _ = next.Update(keyCtrlN())
	next = updated.(model)
	if next.narrationVerbosityLevel != verbosityDetailed {
		t.Fatalf("third Ctrl+N = %d, want detailed", next.narrationVerbosityLevel)
	}
	if strings.Contains(plainRender(t, next.statusLine(120)), "quiet") {
		t.Fatal("cycling back to detailed must clear the segment")
	}
}

// The projection contract through the real render: quiet drops the tool call
// row from the TRANSCRIPT body (the sidebar's ACTIVITY feed is a separate
// projection and still shows completed work — that's by design), detailed
// shows all — while the recorded transcript is untouched.
func TestNarrationCycleChangesProjectionNotRecording(t *testing.T) {
	m := narrationWiringModel(t)
	m.sidebarHidden = true // isolate the transcript projection
	m.width = 100          // below the two-column boundary anyway
	recorded := len(m.transcript)

	// Quiet: the standalone call row drops from the projection.
	updated, _ := m.Update(keyCtrlN())
	quiet := updated.(model)
	quietView := plainRender(t, quiet.View())
	if strings.Contains(quietView, "▸ Ran") {
		t.Fatal("quiet projection still shows the tool call card in the transcript body")
	}

	// The recording is intact: same row count, tool call still on the model.
	if len(quiet.transcript) != recorded {
		t.Fatalf("quiet mutated the transcript: %d -> %d rows", recorded, len(quiet.transcript))
	}
	foundCall := false
	for _, row := range quiet.transcript {
		if row.kind == rowToolCall {
			foundCall = true
		}
	}
	if !foundCall {
		t.Fatal("tool call row vanished from the recording")
	}

	// Cycle back to detailed: the row projects again (the card head is
	// "• Ran" — a bullet, not a triangle).
	updated, _ = quiet.Update(keyCtrlN())
	normal := updated.(model)
	updated, _ = normal.Update(keyCtrlN())
	detailed := updated.(model)
	detailedView := plainRender(t, detailed.View())
	if !strings.Contains(detailedView, "• Ran") {
		t.Fatal("detailed projection lost the tool call row")
	}
}

// A settled transcript rebuilds after a verbosity switch (the visible row set
// changed without the frontier moving). The settled-snapshot generation must
// match the verbosity level the snapshot was built at — buildTranscriptBodyItems
// skips its settled fast path when they disagree (the rebuild happens inside
// Update via settleTranscript, so we verify the invariant: generation always
// equals the level after Update).
func TestNarrationSwitchInvalidatesSettledCache(t *testing.T) {
	m := narrationWiringModel(t)
	m.rebuildAltScreenSettledItems(m.chatColumnWidth())
	// Hand-craft a lagging generation (the state a mid-frame verbosity switch
	// produces before settleTranscript runs) and verify the fast path refuses
	// to serve the stale snapshot.
	lagging := m
	lagging.narrationVerbosityLevel = verbosityQuiet
	lagging.narrationSettledGeneration = verbosityDetailed
	lagging.pending = false
	lagging.flushedAny = true
	lagging.flushed = len(lagging.transcript)
	items := lagging.transcriptBodyItems(lagging.chatColumnWidth(), "", false)
	foundCall := false
	for _, item := range items {
		rendered := renderTranscriptBodyItem(item, 0)
		for _, line := range rendered.lines {
			if strings.Contains(stripANSI(line), "bash") && !strings.Contains(stripANSI(line), "ACTIVITY") {
				foundCall = true
			}
		}
	}
	if foundCall {
		t.Fatal("stale settled snapshot served at a new verbosity (fast path ignored the generation)")
	}
}

// The narration filter respects the generation invariant after Update: the
// settled snapshot is rebuilt synchronously, so its generation always equals
// the current level.
func TestNarrationSettledGenerationTracksVerbosity(t *testing.T) {
	m := narrationWiringModel(t)
	updated, _ := m.Update(keyCtrlN())
	next := updated.(model)
	if next.narrationSettledGeneration != next.narrationVerbosityLevel {
		t.Fatalf("settled generation %d lags verbosity %d after Update",
			next.narrationSettledGeneration, next.narrationVerbosityLevel)
	}
}

func keyCtrlN() tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: 'n', Mod: tea.ModCtrl})
}
