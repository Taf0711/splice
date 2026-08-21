package tui

import (
	"strings"
	"testing"
)

// TestComputePinnedSurfaceEnvelopeReservesChrome pins the coordinator math
// that replaces silent top-clipping: the plan budget is whatever remains after
// the header, the fixed footer chrome, and one minimum transcript row.
func TestComputePinnedSurfaceEnvelopeReservesChrome(t *testing.T) {
	cases := []struct {
		name          string
		total         int
		headerLines   int
		chromeLines   int
		wantAvailable int
	}{
		{name: "roomy", total: 40, headerLines: 1, chromeLines: 6, wantAvailable: 32},
		{name: "exact fit", total: 10, headerLines: 1, chromeLines: 8, wantAvailable: 0},
		{name: "no room for plan", total: 8, headerLines: 1, chromeLines: 6, wantAvailable: 0},
		{name: "tiny terminal", total: 3, headerLines: 1, chromeLines: 2, wantAvailable: 0},
		{name: "unmeasured", total: 0, headerLines: 1, chromeLines: 6, wantAvailable: 0},
		{name: "negative height", total: -4, headerLines: 1, chromeLines: 6, wantAvailable: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := computePinnedSurfaceEnvelope(tc.total, tc.headerLines, tc.chromeLines)
			if env.available != tc.wantAvailable {
				t.Fatalf("available = %d, want %d", env.available, tc.wantAvailable)
			}
		})
	}
}

// TestEnvelopePlanBudgetClampsToHistoricalCap ensures the plan never expands
// to fill freed space (that would change visible output). The envelope may
// only REDUCE the historical one-third cap, never grow it.
func TestEnvelopePlanBudgetClampsToHistoricalCap(t *testing.T) {
	// Roomy envelope: available (32) exceeds the height/3 cap (13) -> cap wins.
	env := computePinnedSurfaceEnvelope(40, 1, 6)
	if got := env.planBudget(13); got != 13 {
		t.Fatalf("planBudget = %d, want cap 13 (plan must not expand)", got)
	}
	// Cramped envelope: available (5) is below the cap (13) -> envelope wins.
	env = computePinnedSurfaceEnvelope(12, 1, 5)
	if got := env.planBudget(4); got != 4 {
		t.Fatalf("planBudget = %d, want envelope 4", got)
	}
	// No room: budget is zero, which must hide the plan entirely.
	env = computePinnedSurfaceEnvelope(8, 1, 6)
	if got := env.planBudget(3); got != 0 {
		t.Fatalf("planBudget = %d, want 0 (plan hidden)", got)
	}
}

// TestFooterChromeCarriesComposerAndStatus verifies the extraction: the chrome
// tail must still contain the composer box (with the typed input) and the
// status line, so the mouse/frame geometry derived from it stays consistent.
func TestFooterChromeCarriesComposerAndStatus(t *testing.T) {
	m := mouseTestModel()
	m.height = 30
	m.copyStatus = ""
	m.input.SetValue("Create a book library dashboard page with cards, filters, charts, and responsive behavior.")

	width := m.chatColumnWidth()
	chrome := plainRender(t, m.footerChrome(width))

	if len(viewLines(chrome)) < 3 {
		t.Fatalf("chrome should carry composer+status+idle rows, got %d lines", len(viewLines(chrome)))
	}
	if !strings.Contains(chrome, "Create a book library dashboard page") {
		t.Fatalf("composer content missing from chrome:\n%s", chrome)
	}
}

// TestFooterPinnedPlanUsesEnvelopeBudget is the end-to-end seam test: on a
// tall terminal the plan still renders full above the composer; on a short
// terminal the plan collapses (or hides) instead of being silently clipped.
func TestFooterPinnedPlanUsesEnvelopeBudget(t *testing.T) {
	m := runningPlanModel(t, 3) // 3 steps -> header + 3 step lines
	m.altScreen = true
	m.headerPrinted = true
	m.height = 40

	width := m.chatColumnWidth()
	footer := plainRender(t, m.footerView(width))
	if !strings.Contains(footer, "Step number 1") {
		t.Fatalf("tall terminal should keep the full plan, got:\n%s", footer)
	}
	composerIdx := strings.Index(footer, "describe a task")
	planIdx := strings.Index(footer, "Step number 1")
	if composerIdx >= 0 && planIdx > composerIdx {
		t.Fatalf("plan should render ABOVE composer, got:\n%s", footer)
	}

	// Short terminal: even a 3-step plan cannot coexist with composer+status,
	// so it must not render full step lines (collapses or hides).
	m.height = 5
	footer = plainRender(t, m.footerView(width))
	if strings.Count(footer, "Step number") > 0 {
		t.Fatalf("short terminal must not render full step lines, got:\n%s", footer)
	}
}

// TestShortTerminalNeverTopClipsPlanSilently is the guard test: when the plan
// renders, the transcript frame must never have had to silently clip the
// footer from the top (footerClip counts rows cut from the top, where the plan
// sits). When the plan is hidden because the envelope gave it zero budget, the
// chrome may still clip at tiny heights under the frame's designed policy
// (pinned by TestTranscriptFrameLayoutClipsFooterInTinyTerminal).
func TestShortTerminalNeverTopClipsPlanSilently(t *testing.T) {
	for _, height := range []int{40, 24, 12, 8, 6, 5, 3, 2, 1} {
		t.Run("h="+string(rune('0'+minInt(9, height))), func(t *testing.T) {
			m := runningPlanModel(t, 3)
			m.altScreen = true
			m.headerPrinted = true
			m.height = height

			width := m.chatColumnWidth()
			footer := m.footerView(width)
			frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), footer)

			if frame.bodyRect.height < 1 {
				t.Fatalf("height %d: body height = %d, want >= 1", height, frame.bodyRect.height)
			}
			// If the plan is visible at all, the frame must not have clipped it:
			// a visible plan plus a top clip means the plan was cut.
			if strings.Contains(footer, "PLAN") && frame.footerClip > 0 {
				t.Fatalf("height %d: visible plan was silently clipped %d rows from the top\nfull footer:\n%s", height, frame.footerClip, footer)
			}
		})
	}
}
