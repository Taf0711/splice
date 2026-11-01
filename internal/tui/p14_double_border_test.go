package tui

// p14_double_border_test.go: the plan/critique/decisions lifecycle cards
// render exactly ONE border. The bug (screenshotted by the owner 2026-09-03):
// the stored row carried a full styledBlock at reference width 100, and
// parseLifecycleCardPayload wrapped that bordered body in styledBlock AGAIN
// at the live width — nested boxes. The fix: stored bodies are border-free;
// the render path is the single border source; legacy bordered rows are
// stripped and re-bordered once.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	splicerun "github.com/Taf0711/splice/internal/splice"
)

// countBoxTopRules counts rendered top borders in a card render.
func countBoxTopRules(rendered string) int {
	count := 0
	for _, line := range strings.Split(rendered, "\n") {
		if strings.HasPrefix(ansi.Strip(line), "╭") {
			count++
		}
	}
	return count
}

// Exactly one top border at any width — the probe that failed before the fix
// (the stored 100-col border rode inside the live-width border).
func TestPlanCardSingleBorder(t *testing.T) {
	tagged := planCardTranscriptText(testPlan(), testCritiqueBlocking())
	render, ok := parseLifecycleCardPayload(tagged)
	if !ok {
		t.Fatal("plan card payload not detected")
	}
	for _, width := range []int{60, 100, 140} {
		if got := countBoxTopRules(render(width)); got != 1 {
			t.Fatalf("plan card at width %d drew %d borders, want 1:\n%s", width, got, stripANSI(render(width)))
		}
	}
}

func TestCritiqueCardSingleBorder(t *testing.T) {
	tagged := critiqueCardTranscriptText(testPlan(), testCritiqueBlocking())
	render, ok := parseLifecycleCardPayload(tagged)
	if !ok {
		t.Fatal("critique card payload not detected")
	}
	if got := countBoxTopRules(render(120)); got != 1 {
		t.Fatalf("critique card drew %d borders, want 1:\n%s", got, stripANSI(render(120)))
	}
}

func TestDecisionsCardSingleBorder(t *testing.T) {
	tagged := decisionsCardTranscriptText([]splicerun.DecisionPinnedPayload{
		{Statement: "retry idempotent methods"},
	})
	render, ok := parseLifecycleCardPayload(tagged)
	if !ok {
		t.Fatal("decisions card payload not detected")
	}
	if got := countBoxTopRules(render(120)); got != 1 {
		t.Fatalf("decisions card drew %d borders, want 1:\n%s", got, stripANSI(render(120)))
	}
}

// Legacy persisted rows (bordered body at reference width) re-border ONCE:
// the old border is stripped, content preserved, live border drawn.
func TestLegacyBorderedCardRendersSingleBorder(t *testing.T) {
	// Build the legacy shape exactly as the old store path did:
	// marker + full styledBlock at width 100.
	legacy := planCardMarker + renderImplementationPlanCard(testPlan(), true, 100)
	render, ok := parseLifecycleCardPayload(legacy)
	if !ok {
		t.Fatal("legacy plan card payload not detected")
	}
	out := render(120)
	if got := countBoxTopRules(out); got != 1 {
		t.Fatalf("legacy bordered row drew %d borders, want 1:\n%s", got, stripANSI(out))
	}
	plain := stripANSI(out)
	for _, want := range []string{"IMPLEMENTATION PLAN", "classify retries"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("legacy reflow lost %q:\n%s", want, plain)
		}
	}
}

// The content survives the border strip: no line of the inner body is eaten
// by the frame removal (fail-visible rule in stripBlockBorder).
func TestStripBlockBorderKeepsContent(t *testing.T) {
	lines := []string{
		"╭" + strings.Repeat("─", 30) + "╮",
		"│ " + "HEADER LINE" + strings.Repeat(" ", 14) + "│",
		"│ " + "  body row" + strings.Repeat(" ", 22) + "│",
		"╰" + strings.Repeat("─", 30) + "╯",
	}
	inner := stripBlockBorder(lines)
	if len(inner) != 2 {
		t.Fatalf("inner lines = %d, want 2: %q", len(inner), inner)
	}
	if !strings.Contains(ansi.Strip(inner[0]), "HEADER LINE") || !strings.Contains(ansi.Strip(inner[1]), "body row") {
		t.Fatalf("strip lost content: %q", inner)
	}
}
