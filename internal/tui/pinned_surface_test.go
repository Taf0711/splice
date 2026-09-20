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
	// The pinned plan is the plan's home only where the sidebar CANNOT host it
	// (below the 120-col two-column boundary since P12 made the launch screen
	// two-column). Pin the frame narrow so the envelope allocator is exercised.
	m.width = 100

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

// TestAllocatePinnedSurfacesExactSkipLeavesSpace pins the strip rule: a strip
// taller than the remaining envelope is skipped whole, and the plan still
// receives everything left instead of being starved by a partial strip.
func TestAllocatePinnedSurfacesExactSkipLeavesSpace(t *testing.T) {
	env := pinnedSurfaceEnvelope{available: 6}
	grants := allocatePinnedSurfaces(env, []pinnedSurfaceClaim{
		{name: "pipeline-strip", lines: 8, exact: true},
		{name: "plan", lines: 6},
	})
	if grants[0].lines != 0 {
		t.Fatalf("strip grant = %d, want 0 (does not fit)", grants[0].lines)
	}
	if grants[1].lines != 6 {
		t.Fatalf("plan grant = %d, want full 6 (skip must not starve later claims)", grants[1].lines)
	}
}

// TestAllocatePinnedSurfacesFlexibleTakesRemaining pins the plan rule: a
// flexible claim takes what remains after earlier claims, capped by its own
// request.
func TestAllocatePinnedSurfacesFlexibleTakesRemaining(t *testing.T) {
	env := pinnedSurfaceEnvelope{available: 10}
	grants := allocatePinnedSurfaces(env, []pinnedSurfaceClaim{
		{name: "pipeline-strip", lines: 3, exact: true},
		{name: "plan", lines: 10},
	})
	if grants[0].lines != 3 || grants[1].lines != 7 {
		t.Fatalf("grants = %d/%d, want strip 3 then plan 7", grants[0].lines, grants[1].lines)
	}
	// A flexible claim smaller than the remainder stops at its request.
	grants = allocatePinnedSurfaces(env, []pinnedSurfaceClaim{
		{name: "a", lines: 2, exact: true},
		{name: "b", lines: 4},
	})
	if grants[1].lines != 4 {
		t.Fatalf("flexible grant = %d, want request 4", grants[1].lines)
	}
}

// TestAllocatePinnedSurfacesZeroEnvelopeGrantsNothing pins the short-height
// guard: zero available renders no surface rather than clipping from the top.
func TestAllocatePinnedSurfacesZeroEnvelopeGrantsNothing(t *testing.T) {
	env := pinnedSurfaceEnvelope{}
	grants := allocatePinnedSurfaces(env, []pinnedSurfaceClaim{
		{name: "pipeline-strip", lines: 2, exact: true},
		{name: "plan", lines: 9},
	})
	for _, grant := range grants {
		if grant.lines != 0 {
			t.Fatalf("grant %s = %d, want 0 at zero envelope", grant.name, grant.lines)
		}
	}
}

// TestAllocatePinnedSurfacesOrderIsPriorityAndExtends pins the modular seam:
// order decides priority, and a third surface joins by appending a claim —
// no footer math changes required.
func TestAllocatePinnedSurfacesOrderIsPriorityAndExtends(t *testing.T) {
	env := pinnedSurfaceEnvelope{available: 5}
	grants := allocatePinnedSurfaces(env, []pinnedSurfaceClaim{
		{name: "first", lines: 4, exact: true},
		{name: "second", lines: 4, exact: true},
		{name: "third", lines: 2},
	})
	if grants[0].lines != 4 || grants[1].lines != 0 || grants[2].lines != 1 {
		t.Fatalf("grants = %v, want first 4, second skipped, third remainder 1", grants)
	}
}

// TestFooterPinnedAllocationMatchesLegacyFormula is the equivalence property
// for the TP6 refactor: across a grid of heights, chrome and header sizes,
// and strip lengths, the allocator must hand out exactly the budgets the
// previous inline footer math computed. Any drift here is a visible-output
// regression.
func TestFooterPinnedAllocationMatchesLegacyFormula(t *testing.T) {
	legacyBudgets := func(total, headerLines, chromeLines, stripLen int) (stripBudget, planBudget int) {
		if total <= 0 {
			return 0, 0
		}
		envelope := computePinnedSurfaceEnvelope(total, headerLines, chromeLines)
		if stripLen > 0 && stripLen <= envelope.available {
			stripBudget = stripLen
		}
		return stripBudget, envelope.planBudget(maxInt(0, envelope.available-stripBudget))
	}
	for total := 1; total <= 40; total++ {
		for headerLines := 0; headerLines <= 4; headerLines++ {
			for chromeLines := 0; chromeLines <= 8; chromeLines++ {
				for stripLen := 0; stripLen <= 8; stripLen++ {
					wantStrip, wantPlan := legacyBudgets(total, headerLines, chromeLines, stripLen)
					envelope := computePinnedSurfaceEnvelope(total, headerLines, chromeLines)
					var claims []pinnedSurfaceClaim
					if stripLen > 0 {
						claims = append(claims, pinnedSurfaceClaim{name: "pipeline-strip", lines: stripLen, exact: true})
					}
					claims = append(claims, pinnedSurfaceClaim{name: "plan", lines: envelope.available})
					grants := allocatePinnedSurfaces(envelope, claims)
					gotStrip, gotPlan := 0, 0
					for _, grant := range grants {
						switch grant.name {
						case "pipeline-strip":
							gotStrip = grant.lines
						case "plan":
							gotPlan = grant.lines
						}
					}
					if gotStrip != wantStrip || gotPlan != wantPlan {
						t.Fatalf("total=%d hdr=%d chrome=%d strip=%d: allocator=(%d,%d) legacy=(%d,%d)",
							total, headerLines, chromeLines, stripLen, gotStrip, gotPlan, wantStrip, wantPlan)
					}
				}
			}
		}
	}
}
