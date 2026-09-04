package tui

// p14_stages_design_test.go: the /stages wizard speaks the /model picker's
// design language (owner: "look at model command for the new design and fold
// that in"). Probes: ❯ selection markers, theme rules (not ASCII dashes), a
// detail block keyed to the highlighted row, scroll affordances, a hint bar
// that names only keys the current state responds to, and content-derived
// overlay width. The state machine is untouched — render-only.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func stagesPlain(t *testing.T, lines []string) string {
	t.Helper()
	plain := make([]string, len(lines))
	for i, line := range lines {
		plain[i] = ansi.Strip(line)
	}
	return strings.Join(plain, "\n")
}

func ansiStrip(s string) string {
	return ansi.Strip(s)
}

// The overview renders the ❯ marker on the selected row (matching /model),
// never the old ASCII "> ".
func TestStagesOverviewSelectionMarker(t *testing.T) {
	wizard := stageWizardFixture(t)
	plain := stagesPlain(t, wizard.renderOverview(80))
	if !strings.Contains(plain, "❯ default") {
		t.Fatalf("overview missing the ❯ marker on the selected row:\n%s", plain)
	}
	if strings.Contains(plain, "\n> ") {
		t.Fatalf("overview still renders the ASCII '> ' marker:\n%s", plain)
	}
}

// The detail block sits under a theme rule and describes the highlighted
// routing target — the /model keyed-detail pattern.
func TestStagesOverviewDetailBlock(t *testing.T) {
	wizard := stageWizardFixture(t)
	plain := stagesPlain(t, wizard.renderOverview(80))
	for _, want := range []string{
		"────────",                             // theme rule
		"every stage without its own override", // default's role
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("overview detail block missing %q:\n%s", want, plain)
		}
	}
	// Highlight a stage row: the detail swaps to the stage's description.
	wizard.overviewCursor = 2 // code_writer
	plain = stagesPlain(t, wizard.renderOverview(80))
	if !strings.Contains(plain, "writes and modifies code") {
		t.Fatalf("stage detail missing the description:\n%s", plain)
	}
}

// The overview list advertises a clipped window instead of letting its edge
// read as the end of the list.
func TestStagesOverviewScrollAffordances(t *testing.T) {
	wizard := stageWizardFixture(t)
	// The fixture has 4 rows (default, escalation, code_writer, test_generator)
	// — under the 12-row window, so no affordances.
	plain := stagesPlain(t, wizard.renderOverview(80))
	if strings.Contains(plain, "more below") {
		t.Fatalf("short list rendered a scroll hint:\n%s", plain)
	}
	// Force a long list: many unknown extension stages.
	wizard.config.Stages = map[string]schemas.StageModelConfig{}
	for i := 0; i < 15; i++ {
		wizard.config.Stages["ext_stage_"+string(rune('a'+i))] = schemas.StageModelConfig{ProviderProfile: "openai", Model: "m"}
	}
	wizard.overviewCursor = 0
	plain = stagesPlain(t, wizard.renderOverview(80))
	if !strings.Contains(plain, "↓ more below") {
		t.Fatalf("clipped list missing the below hint:\n%s", plain)
	}
	wizard.overviewCursor = len(wizard.overviewRows()) - 1
	plain = stagesPlain(t, wizard.renderOverview(80))
	if !strings.Contains(plain, "↑ more above") {
		t.Fatalf("scrolled list missing the above hint:\n%s", plain)
	}
}

// The hint bar names only keys the current state responds to: `s save` appears
// only when an override actually changed, `d delete` only on the overview.
func TestStagesHintBarContextual(t *testing.T) {
	wizard := stageWizardFixture(t)
	lines := wizard.footerLines(120)
	plain := stagesPlain(t, lines)
	if strings.Contains(plain, "s save") {
		t.Fatalf("pristine overview advertised save:\n%s", plain)
	}
	if !strings.Contains(plain, "d delete") || !strings.Contains(plain, "⏎ edit") {
		t.Fatalf("overview hint bar missing its keys:\n%s", plain)
	}
	// Dirty state: save appears.
	wizard.config.Stages = map[string]schemas.StageModelConfig{
		"code_writer": {ProviderProfile: "openai", Model: "gpt-4.1"},
	}
	plain = stagesPlain(t, wizard.footerLines(120))
	if !strings.Contains(plain, "s save") {
		t.Fatalf("dirty overview missing the save hint:\n%s", plain)
	}
	// Narrow width sheds to the core keys.
	plain = stagesPlain(t, wizard.footerLines(30))
	if strings.Contains(plain, "s save") || strings.Contains(plain, "d delete") {
		t.Fatalf("narrow hint bar did not shed optional keys:\n%s", plain)
	}
	if !strings.Contains(plain, "⏎ edit") {
		t.Fatalf("narrow hint bar lost the core keys:\n%s", plain)
	}
	// Edit state: no save/delete, says open.
	wizard.step = stageModelWizardStepEditStage
	wizard.editTarget = "code_writer"
	plain = stagesPlain(t, wizard.footerLines(120))
	if strings.Contains(plain, "s save") || strings.Contains(plain, "d delete") {
		t.Fatalf("edit state leaked overview-only keys:\n%s", plain)
	}
	if !strings.Contains(plain, "⏎ open") {
		t.Fatalf("edit state hint bar missing open:\n%s", plain)
	}
}

// The overlay sizes from content, not a fixed width: a short row set yields a
// narrower overlay than the ceiling.
func TestStagesOverlayWidthFromContent(t *testing.T) {
	wizard := stageWizardFixture(t)
	w := stageModelOverlayWidth(200, wizard)
	if w > stageModelWizardWidth {
		t.Fatalf("overlay width %d exceeds the ceiling %d", w, stageModelWizardWidth)
	}
	if w >= 92 {
		t.Fatalf("content-sized overlay hit the fixed-92 legacy width: %d", w)
	}
	// Never below the readable floor.
	if w < stageModelWizardMinWidth {
		t.Fatalf("overlay width %d below the floor %d", w, stageModelWizardMinWidth)
	}
}

// The full render carries the themed frame: title bar, no ASCII-dash rules,
// and the ❯ marker — one visual language with /model.
func TestStagesRenderDesignLanguage(t *testing.T) {
	m := model{stageModelWizard: stageWizardFixture(t)}
	block := m.stageModelWizardOverlay(120)
	plain := ansiStrip(block)
	if !strings.Contains(plain, "Stage model routing") {
		t.Fatalf("render missing the title:\n%s", plain)
	}
	if strings.Contains(plain, "\n---") {
		t.Fatalf("render still uses ASCII-dash rules:\n%s", plain)
	}
	if !strings.Contains(plain, "╭") || !strings.Contains(plain, "╰") {
		t.Fatalf("render missing the rounded frame:\n%s", plain)
	}
	if strings.Contains(plain, "[overview]") {
		t.Fatalf("render still carries the bracket step line:\n%s", plain)
	}
}
