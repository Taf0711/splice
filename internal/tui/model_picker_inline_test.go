package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/modelregistry"
)

// modelPickerFixture opens a /model picker with the cursor on modelID.
func modelPickerFixture(t *testing.T, activeModel, cursorModel string) model {
	t.Helper()
	m := limeTestModel()
	m.modelName = activeModel
	picker := m.newModelPicker()
	if picker == nil {
		t.Fatal("expected a model picker")
	}
	m.picker = picker
	for index, item := range picker.items {
		if item.Value == cursorModel {
			picker.selected = index
			return m
		}
	}
	t.Fatalf("model %q not present in picker", cursorModel)
	return m
}

// The ring opens on the model's own declared default, so ←/→ adjusts relative to
// what the user would actually get rather than an arbitrary tier.
func TestModelPickerRingOpensOnModelDefault(t *testing.T) {
	m := modelPickerFixture(t, "gpt-4o", "claude-sonnet-4.5")
	item, _ := m.picker.current()
	if got := item.effortLabel(); got != "medium" {
		t.Fatalf("claude-sonnet-4.5 default effort = %q, want medium", got)
	}
}

// The row for the model already in use must reflect the session's live effort,
// not the catalog default — otherwise reopening /model would misreport the state
// and a stray arrow would revert a deliberate /effort choice.
func TestModelPickerActiveRowShowsSessionEffort(t *testing.T) {
	m := limeTestModel()
	m.modelName = "claude-sonnet-4.5"
	m.reasoningEffort = modelregistry.ReasoningEffortLow
	picker := m.newModelPicker()
	for _, item := range picker.items {
		if item.Value != "claude-sonnet-4.5" {
			continue
		}
		if got := item.effortLabel(); got != "low" {
			t.Fatalf("active row effort = %q, want the session's low", got)
		}
		return
	}
	t.Fatal("active model missing from picker")
}

// ←/→ steps the ring and clamps at both ends: holding an arrow settles at a
// boundary instead of wrapping around through every tier.
func TestModelPickerEffortClampsAtBothEnds(t *testing.T) {
	m := modelPickerFixture(t, "gpt-4o", "claude-sonnet-4.5")
	for i := 0; i < 8; i++ {
		m, _ = m.adjustModelPickerEffort(1)
	}
	item, _ := m.picker.current()
	if got := item.effortLabel(); got != "high" {
		t.Fatalf("after stepping up repeatedly effort = %q, want the top tier high", got)
	}
	if _, moved := m.adjustModelPickerEffort(1); moved {
		t.Fatal("→ at the top of the ring must be a no-op, not a wrap")
	}
	for i := 0; i < 8; i++ {
		m, _ = m.adjustModelPickerEffort(-1)
	}
	item, _ = m.picker.current()
	if got := item.effortLabel(); got != "auto" {
		t.Fatalf("after stepping down repeatedly effort = %q, want auto", got)
	}
	if _, moved := m.adjustModelPickerEffort(-1); moved {
		t.Fatal("← at auto must be a no-op, not a wrap")
	}
}

// A model with no effort controls ignores ←/→ rather than inventing a ring.
func TestModelPickerEffortNoopWithoutRing(t *testing.T) {
	m := modelPickerFixture(t, "claude-sonnet-4.5", "gpt-4o")
	if _, moved := m.adjustModelPickerEffort(1); moved {
		t.Fatal("gpt-4o exposes no reasoning efforts; ←→ must not move")
	}
}

// An effort dialed in before filtering must survive the filter. applyQuery
// rebuilds items from allItems, so the edit has to reach both lists or typing a
// search would silently discard it.
func TestModelPickerEffortSurvivesQueryFilter(t *testing.T) {
	m := modelPickerFixture(t, "gpt-4o", "claude-sonnet-4.5")
	m, _ = m.adjustModelPickerEffort(1)
	before, _ := m.picker.current()
	if before.effortLabel() != "high" {
		t.Fatalf("setup: effort = %q, want high", before.effortLabel())
	}
	m.picker.appendQuery([]rune("sonnet"))
	m.picker.deleteQueryRune()
	m.picker.appendQuery([]rune("t"))
	for _, item := range m.picker.items {
		if item.Value != "claude-sonnet-4.5" {
			continue
		}
		if got := item.effortLabel(); got != "high" {
			t.Fatalf("effort after filtering = %q, want the high set before filtering", got)
		}
		return
	}
	t.Fatal("filtered list dropped the row under test")
}

// Enter commits the effort the row was left on — the whole point of the inline
// ring is that it does not need a second trip through /effort.
func TestModelPickerChooseCommitsEffort(t *testing.T) {
	m := limeTestModel()
	m.modelName = "claude-sonnet-4.5"
	item := pickerItem{
		Value:       "claude-sonnet-4.5",
		Efforts:     m.modelCatalog.ReasoningEfforts("claude-sonnet-4.5"),
		EffortIndex: 2,
	}
	if got := item.effortLabel(); got != "high" {
		t.Fatalf("setup: row effort = %q, want high", got)
	}
	if got := m.applyPickedModelEffort(item).reasoningEffort; got != modelregistry.ReasoningEffortHigh {
		t.Fatalf("committed effort = %q, want high", got)
	}
}

// A row left on auto must not force an effort — auto means "let the model's own
// default apply", which is handleModelCommand's job, not the picker's.
func TestModelPickerChooseOnAutoLeavesEffortUnset(t *testing.T) {
	m := modelPickerFixture(t, "gpt-4o", "claude-sonnet-4.5")
	for i := 0; i < 8; i++ {
		m, _ = m.adjustModelPickerEffort(-1)
	}
	next, _ := m.choosePicker()
	committed := next.(model)
	if committed.reasoningEffort != "" {
		t.Fatalf("effort = %q, want unset for a row left on auto", committed.reasoningEffort)
	}
}

// A refused model switch must not leave the effort behind. The ring was dialed
// in for the model on the row; applying it to the model that is still active
// would retune something the user never selected.
func TestModelPickerEffortNotAppliedWhenSwitchRefused(t *testing.T) {
	m := limeTestModel()
	m.modelName = "claude-opus-4.1" // switch to sonnet is refused; both support "high"
	item := pickerItem{
		Value:       "claude-sonnet-4.5",
		Efforts:     m.modelCatalog.ReasoningEfforts("claude-sonnet-4.5"),
		EffortIndex: 2,
	}
	if got := m.applyPickedModelEffort(item).reasoningEffort; got != "" {
		t.Fatalf("effort = %q, want unset: the model switch did not land", got)
	}
}

// Tab only responds on rows that advertise long context, matching the toggle
// line that appears there — an inert key with a visible label would mislead.
func TestModelPickerContextToggleOnlyWhereSupported(t *testing.T) {
	m := modelPickerFixture(t, "gpt-4o", "gpt-5.6-sol")
	m, toggled := m.toggleModelPickerContext()
	if !toggled {
		t.Fatal("gpt-5.6-sol advertises long context; tab must toggle it")
	}
	item, _ := m.picker.current()
	if !item.LongContextOn {
		t.Fatal("toggle did not stick")
	}
	if !strings.Contains(plainRender(t, renderContextToggle(item)), "On") {
		t.Fatal("toggle line must show the On state")
	}

	other := modelPickerFixture(t, "gpt-4o", "claude-sonnet-4.5")
	if _, toggled := other.toggleModelPickerContext(); toggled {
		t.Fatal("claude-sonnet-4.5 has no long-context capability; tab must be inert")
	}
	if got := renderContextToggle(mustCurrent(t, other)); got != "" {
		t.Fatalf("no toggle line expected for an unsupported row, got %q", got)
	}
}

func mustCurrent(t *testing.T, m model) pickerItem {
	t.Helper()
	item, ok := m.picker.current()
	if !ok {
		t.Fatal("no highlighted row")
	}
	return item
}

// The ring must encode its level in SHAPE, not only color: under NO_COLOR (and
// in low-contrast terminals) a brightness-only bar would carry no information.
func TestModelPickerRingReadableWithoutColor(t *testing.T) {
	item := pickerItem{
		Label:       "Test",
		Efforts:     []modelregistry.ReasoningEffort{"low", "medium", "high"},
		EffortIndex: 0,
	}
	plain := plainRender(t, renderEffortRing(item, false, transparentSurface))
	if !strings.Contains(plain, effortSegmentFilled) || !strings.Contains(plain, effortSegmentEmpty) {
		t.Fatalf("ring = %q, must distinguish filled from empty without color", plain)
	}
	full := pickerItem{Label: "Test", Efforts: item.Efforts, EffortIndex: 2}
	if plainRender(t, renderEffortRing(full, false, transparentSurface)) == plain {
		t.Fatal("a low ring and a high ring must not render identically without color")
	}
}

// Ring lengths differ per model, and the column exists so those lengths can be
// compared down the list. That only works if every ring starts at one x — so a
// selected row (which gains ← →) must not shift its ring relative to its
// neighbours.
func TestModelPickerRingColumnStableAcrossSelection(t *testing.T) {
	item := pickerItem{
		Label:       "Test",
		Efforts:     []modelregistry.ReasoningEffort{"low", "medium", "high"},
		EffortIndex: 1,
	}
	unselected := plainRender(t, renderModelPickerRow(70, false, item))
	selected := plainRender(t, renderModelPickerRow(70, true, item))
	if ringColumn(unselected) != ringColumn(selected) {
		t.Fatalf("ring column shifts with selection:\nunselected=%q (col %d)\nselected=  %q (col %d)",
			unselected, ringColumn(unselected), selected, ringColumn(selected))
	}
}

// ringColumn is the display column of the ring's first filled segment. It counts
// runes, not bytes: the selected row's "❯" marker is multi-byte, so a byte
// offset would report a shift that the terminal never shows.
func ringColumn(row string) int {
	for column, r := range []rune(row) {
		if string(r) == effortSegmentFilled {
			return column
		}
	}
	return -1
}

// The rail is a comparison axis: a cheaper model must land left of a pricier one.
func TestCostRailOrdersCheapBeforeExpensive(t *testing.T) {
	cheap := pickerCost{InputPerMillion: 0.1, OutputPerMillion: 0.4}
	dear := pickerCost{InputPerMillion: 15, OutputPerMillion: 75}
	low, high := cheap.blendedRate(), dear.blendedRate()
	if got := costRailPosition(cheap.blendedRate(), low, high); got != 0 {
		t.Fatalf("cheapest model position = %v, want the left end", got)
	}
	if got := costRailPosition(dear.blendedRate(), low, high); got != 1 {
		t.Fatalf("priciest model position = %v, want the right end", got)
	}
	mid := costRailPosition(pickerCost{InputPerMillion: 3, OutputPerMillion: 15}.blendedRate(), low, high)
	if mid <= 0 || mid >= 1 {
		t.Fatalf("a mid-priced model must land strictly inside the rail, got %v", mid)
	}
}

// The rail carries two knobs — active and hovered — and they must be
// distinguishable by glyph so the comparison survives a monochrome terminal.
func TestCostRailMarksActiveAndHovered(t *testing.T) {
	plain := plainRender(t, renderCostRail(30, 0.9, 0.1, true))
	if !strings.Contains(plain, "●") || !strings.Contains(plain, "○") {
		t.Fatalf("rail = %q, want distinct hovered and active knobs", plain)
	}
	if strings.Index(plain, "○") >= strings.Index(plain, "●") {
		t.Fatal("the cheaper active model must render left of the pricier hovered one")
	}
	solo := plainRender(t, renderCostRail(30, 0.5, 0, false))
	if strings.Contains(solo, "○") {
		t.Fatal("no active knob should render when the active model is absent or unpriced")
	}
}

// Zero rates render as one "Free" line, not three "$0 / 1M" columns.
func TestPriceColumnsCollapseWhenFree(t *testing.T) {
	if got := plainRender(t, renderPriceColumns(pickerCost{})); got != "Free" {
		t.Fatalf("free model price readout = %q, want %q", got, "Free")
	}
	paid := plainRender(t, renderPriceColumns(pickerCost{InputPerMillion: 3, CachedInputPerMillion: 0.3, OutputPerMillion: 15}))
	for _, want := range []string{"$3 / 1M", "$0.3 / 1M", "$15 / 1M"} {
		if !strings.Contains(paid, want) {
			t.Fatalf("price readout = %q, missing %q", paid, want)
		}
	}
}

// Rates print without trailing-zero noise at the top end while keeping the cents
// that distinguish models at the cheap end.
func TestFormatRatePerMillion(t *testing.T) {
	cases := map[float64]string{
		0:     "$0 / 1M",
		0.002: "$0.002 / 1M",
		0.26:  "$0.26 / 1M",
		1.5:   "$1.5 / 1M",
		12:    "$12 / 1M",
	}
	for rate, want := range cases {
		if got := formatRatePerMillion(rate); got != want {
			t.Errorf("formatRatePerMillion(%v) = %q, want %q", rate, got, want)
		}
	}
}

// The hint bar must never render truncated: it sheds optional entries until it
// fits, so Esc — which lives at the tail — always survives.
func TestModelPickerHintBarShedsRatherThanTruncates(t *testing.T) {
	item := pickerItem{
		Efforts:     []modelregistry.ReasoningEffort{"low", "medium", "high"},
		LongContext: true,
	}
	core := modelPickerHintBar(pickerItem{}, false, 1)
	for _, width := range []int{20, 30, 45, 60, 80, 120} {
		bar := modelPickerHintBar(item, true, width)
		// Below the core's own width there is nothing left to shed, so the core is
		// the floor; above it the bar must fit.
		if lipgloss.Width(bar) > width && bar != core {
			t.Fatalf("width %d: bar %q overflows without having shed to the core %q", width, bar, core)
		}
		for _, must := range []string{"↑↓ select", "⏎ confirm", "esc cancel"} {
			if !strings.Contains(bar, must) {
				t.Fatalf("width %d: bar %q dropped the core key %q", width, bar, must)
			}
		}
	}
	wide := modelPickerHintBar(item, true, 200)
	if !strings.Contains(wide, "←→ effort") {
		t.Fatalf("a wide bar should advertise the effort keys, got %q", wide)
	}
}

// A row the registry has no pricing for says so, rather than rendering an empty
// rail that would read as free.
func TestModelPickerUnpricedRowStatesUnknown(t *testing.T) {
	m := modelPickerFixture(t, "gpt-4o", "claude-sonnet-4.5")
	item, _ := m.picker.current()
	item.Cost = nil
	got := plainRender(t, strings.Join(m.modelPickerPriceLines(item, 60, "  "), "\n"))
	if !strings.Contains(got, "pricing unknown") {
		t.Fatalf("unpriced row = %q, want an explicit unknown-pricing note", got)
	}
	if strings.Contains(got, "Free") {
		t.Fatal("missing pricing must never render as Free")
	}
}
