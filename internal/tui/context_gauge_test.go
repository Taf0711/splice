package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/usage"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// The context-fill gauge is empty before any request, then shows
// used/window · pct from the last turn's input tokens against the model window.
func TestContextWindowSegment(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "claude-sonnet-4.5"})
	if got := m.contextWindowSegment(); got != "" {
		t.Fatalf("expected empty gauge before any request, got %q", got)
	}
	// 161k latest-step tokens against the 200k window = 81%.
	if _, err := m.usageTracker.Record(usage.RecordInput{
		ModelID: "claude-sonnet-4.5",
		Usage:   zeroruntime.Usage{InputTokens: 160_000, OutputTokens: 1000},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got := plainRender(t, m.contextWindowSegment())
	if !strings.Contains(got, "161K/200K") || !strings.Contains(got, "81%") {
		t.Fatalf("gauge = %q, want 161K/200K · 81%%", got)
	}
}

// TestStatusLineDoesNotDuplicateTokenFigureNextToGauge: the gauge's "used"
// figure and the plain usage segment's token count both read the exact same
// latestUsageTokens value — once the gauge is showing it, the usage segment
// must fall back to cost-only, or the same number renders twice side by
// side in the footer (e.g. "◔ 161K/200K · 81% │ 161K tok").
func TestStatusLineDoesNotDuplicateTokenFigureNextToGauge(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "claude-sonnet-4.5"})
	if _, err := m.usageTracker.Record(usage.RecordInput{
		ModelID: "claude-sonnet-4.5",
		Usage:   zeroruntime.Usage{InputTokens: 160_000, OutputTokens: 1000},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	status := plainRender(t, m.statusLine(110))
	if !strings.Contains(status, "161K/200K") {
		t.Fatalf("status line = %q, expected the context gauge to render", status)
	}
	if strings.Count(status, "161K") != 1 {
		t.Fatalf("status line = %q, the 161K token figure must appear exactly once, not duplicated next to the gauge", status)
	}
	if strings.Contains(status, "tok") {
		t.Fatalf("status line = %q, the plain token segment should be suppressed once the gauge shows the same figure", status)
	}
}

// The context figure is the context-window fill of the latest request. RUN
// does not repeat it; the bottom sidebar line keeps it with the % gauge.
func TestSidebarRunOmitsContextRowKeepsBottomFigure(t *testing.T) {
	base := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	m := newModel(t.Context(), Options{ModelName: "claude-sonnet-4.5"})
	if _, err := m.usageTracker.Record(usage.RecordInput{
		ModelID: "claude-sonnet-4.5",
		Usage:   zeroruntime.Usage{InputTokens: 160_000, OutputTokens: 1000},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	m.now = func() time.Time { return base.Add(12 * time.Second) }
	m.activeRunID = 7
	m.pending = true
	m.turnStartedAt = base
	// RUN carries no token row: the figure is shown once, on the bottom line
	// with its percent gauge.
	if plain := stripSidebar(m.renderContextModules(40, 60)); strings.Contains(plain, "161K / 200K tokens") {
		t.Fatalf("RUN module repeats the token figure:\n%s", plain)
	}
	bottom := stripSidebar([]string{m.sidebarTokenLine(40)})
	if !strings.Contains(bottom, "161K / 200K tokens") || !strings.Contains(bottom, "%") {
		t.Fatalf("bottom line lost the token figure or gauge: %q", bottom)
	}
}
