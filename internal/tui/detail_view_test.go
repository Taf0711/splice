package tui

// detail_view_test.go (P3 GAP-G rest, §17 Ctrl+O): the detail/evidence pane
// wired through the real Update path — open/close toggle, the body swap at
// the single source, runtime-truth-only content, and Esc restore.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/presentation"
)

func detailPaneState() presentation.State {
	return presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Evidence: []presentation.EvidenceGroup{
			{Label: "tests", Status: presentation.EvidencePassed, Passed: 12, Failed: 0, Duration: 3.2,
				Findings: []string{"all suites green"}},
			{Label: "security", Status: presentation.EvidenceFailed, Passed: 4, Failed: 1, Duration: 1.1,
				Findings: []string{"hardcoded credential in retry.go"}},
		},
		Interventions: []presentation.Intervention{
			{Kind: presentation.InterventionRollback, TargetNodeID: "test_runner", Status: presentation.InterventionProposed, Reason: "2 failures exceed the floor"},
		},
		Completion: &presentation.CompletionReceipt{Status: "failed", Detail: "test_runner failed", Staged: 5, Applied: 0},
	}
}

// Ctrl+G opens the pane, swaps the body to the evidence projection, and
// Ctrl+G again closes it restoring the scroll position. (Ctrl+O keeps its
// pinned detailed-transcript contract; the pane gets its own chord.)
func TestDetailPaneToggleOnCtrlO(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.lastState = detailPaneState()
	m.chatScrollOffset = 7

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'g', Mod: tea.ModCtrl}))
	open := updated.(model)
	if !open.detailView.active {
		t.Fatal("Ctrl+G did not open the detail pane")
	}
	if open.chatScrollOffset != 0 {
		t.Fatalf("open pane did not reset scroll: %d", open.chatScrollOffset)
	}
	// The body swap is real: buildTranscriptBodyItems returns the pane block.
	items := open.buildTranscriptBodyItems(100, "", false)
	if len(items) != 1 {
		t.Fatalf("pane body swap produced %d items", len(items))
	}
	plain := stripANSI(strings.Join(items[0].render(0).lines, "\n"))
	for _, want := range []string{"EVIDENCE", "tests", "12 pass", "security", "INTERVENTIONS", "test_runner", "RECEIPT", "staged 5 · applied 0"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("pane missing %q:\n%s", want, plain)
		}
	}

	updated, _ = open.Update(tea.KeyPressMsg(tea.Key{Code: 'g', Mod: tea.ModCtrl}))
	closed := updated.(model)
	if closed.detailView.active {
		t.Fatal("second Ctrl+G did not close the pane")
	}
	if closed.chatScrollOffset != 7 {
		t.Fatalf("close did not restore scroll: got %d want 7", closed.chatScrollOffset)
	}
}

// Esc closes the pane too (the drill-in contract), and the title bar swaps
// to the pane nav while open.
func TestDetailPaneEscAndNavBar(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.lastState = detailPaneState()
	m = m.openDetailView()
	m.altScreen = true
	m.height = 40

	nav := m.pinnedTitleBar(120)
	if !strings.Contains(stripANSI(nav), "DETAIL — run evidence") || !strings.Contains(stripANSI(nav), "2 evidence groups") {
		t.Fatalf("nav bar missing header/count: %q", stripANSI(nav))
	}

	updated, _ := m.Update(testKey(tea.KeyEsc))
	closed := updated.(model)
	if closed.detailView.active {
		t.Fatal("Esc did not close the pane")
	}
}

// Projection only, from runtime truth: an empty lastState renders the honest
// empty notice — never invented evidence.
func TestDetailPaneEmptyStateRendersNotice(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m = m.openDetailView()
	plain := stripANSI(m.renderDetailView(100))
	if !strings.Contains(plain, "No run evidence yet") {
		t.Fatalf("empty state missing the notice:\n%s", plain)
	}
	if strings.Contains(plain, "EVIDENCE") {
		t.Fatalf("empty state invented an evidence section:\n%s", plain)
	}
}

// Session switch resets the pane (run-bound interaction state).
func TestDetailPaneResetsOnSessionSwitch(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m = m.openDetailView()
	m = m.resetRunInteractionState()
	if m.detailView.active {
		t.Fatal("session switch left the detail pane open")
	}
}

// Findings are capped so a runaway suite cannot freeze the pane.
func TestDetailPaneCapsFindings(t *testing.T) {
	findings := make([]string, detailViewMaxFindings+25)
	for i := range findings {
		findings[i] = "finding"
	}
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.lastState = presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Evidence: []presentation.EvidenceGroup{
			{Label: "huge", Status: presentation.EvidencePassed, Passed: len(findings), Findings: findings},
		},
	}
	m = m.openDetailView()
	plain := stripANSI(m.renderDetailView(100))
	if !strings.Contains(plain, "25 more findings not shown") {
		t.Fatalf("finding cap notice missing:\n%s", plain)
	}
}
