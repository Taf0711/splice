package tui

// review_probe_ledger_quiet_test.go (final review, 2026-09-02): interaction
// probe — the decisions ledger card is a rowSystem row, and quiet verbosity
// drops system rows. Dropping the LEDGER at quiet would hide settled
// decisions (anchors) from the quiet view: the transcript "already is the
// record", and anchors are the record's load-bearing part. The ledger card
// must survive every verbosity level.

import (
	"strings"
	"testing"

	splicerun "github.com/Taf0711/splice/internal/splice"
)

func mustPreset(t *testing.T, name string) layoutPreset {
	t.Helper()
	preset, ok := layoutPresetByName(name)
	if !ok {
		t.Fatalf("preset %q missing", name)
	}
	return preset
}

func TestReviewLedgerCardSurvivesQuiet(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m = m.applyLayoutPreset(mustPreset(t, "compact")) // quiet + sidebar hidden

	row := transcriptRow{kind: rowSystem, text: decisionsCardMarker + renderDecisionsCard(nil, 100)}
	if !m.narrationVisibleRow(row) {
		t.Fatal("quiet verbosity dropped the decisions ledger card — anchors must survive every verbosity level")
	}
	// Contrast: ordinary system chatter still drops at quiet.
	chatter := transcriptRow{kind: rowSystem, text: "some transient command output"}
	if m.narrationVisibleRow(chatter) {
		t.Fatal("sanity: ordinary system rows should still drop at quiet")
	}
}

func TestReviewLedgerCardRendersThroughQuietView(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m = m.applyLayoutPreset(mustPreset(t, "compact"))
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowSystem,
		text: decisionsCardMarker + renderDecisionsCard([]splicerun.DecisionPinnedPayload{{Statement: "cap 5s", Revised: true}}, 100),
	})
	plain := plainRender(t, m.View())
	if !strings.Contains(plain, "[~] REVISED") {
		t.Fatal("quiet view lost the ledger's revised marker")
	}
}
