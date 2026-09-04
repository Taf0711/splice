package tui

// p14_delta_test.go (P1.4 Ideal-Iteration delta, spec
// plans/SPEC_P14_IDEAL_ITERATION_DELTA.md): the sidebar DECISIONS module
// projects the reconstructed pin ledger — the same runtime data the
// transcript ledger card reads — and is ABSENT while zero pins exist.
// The frame's "in progress" and open rows need runtime states that do not
// exist (DecisionPinnedPayload has statement/detail/revised only) and stay
// deferred; these probes pin the honest subset that IS buildable.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/sessions"
	splicerun "github.com/Taf0711/splice/internal/splice"
)

func p14DecisionPin(t *testing.T, statement string, revised bool) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(splicerun.DecisionPinnedPayload{Statement: statement, Revised: revised})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// Wire-as-you-go: the sidebar module renders from the SAME reconstructed
// ledger the transcript card uses — settled rows with [+], revised rows with
// the [~] REVISED marker, count in the header.
func TestSidebarDecisionsModuleProjectsPinLedger(t *testing.T) {
	m := mouseTestModel()
	m.sessionEvents = []sessions.Event{
		{Type: sessions.EventDecisionPinned, Payload: p14DecisionPin(t, "retry idempotent methods", false)},
		{Type: sessions.EventDecisionPinned, Payload: p14DecisionPin(t, "preserve deadlines", false)},
		{Type: sessions.EventDecisionPinned, Payload: p14DecisionPin(t, "backoff cap 5s", true)},
	}
	lines := m.renderContextModules(40, 60)
	plain := stripSidebar(lines)
	if !strings.Contains(plain, "DECISIONS") {
		t.Fatalf("sidebar missing the DECISIONS module:\n%s", plain)
	}
	for _, want := range []string{"[+] retry idempotent methods", "[+] preserve deadlines", "[~] REVISED", "backoff cap 5s"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("DECISIONS module missing %q:\n%s", want, plain)
		}
	}
	// Header count covers every pin in the ledger.
	if !strings.Contains(plain, "DECISIONS") || !strings.Contains(plain, "3") {
		t.Fatalf("DECISIONS header count wrong:\n%s", plain)
	}
	// The deferred states must NOT be invented: no in-progress, no open rows.
	if strings.Contains(plain, "in progress") || strings.Contains(plain, "[ ]") {
		t.Fatalf("DECISIONS module invented a deferred state:\n%s", plain)
	}
	// Slot order: DECISIONS sits between PLAN and FILES (after Plan).
	plan := strings.Index(plain, "PLAN")
	decisions := strings.Index(plain, "DECISIONS")
	files := strings.Index(plain, "FILES")
	if !(plan < decisions && decisions < files) {
		t.Fatalf("DECISIONS slot wrong: plan@%d decisions@%d files@%d", plan, decisions, files)
	}
}

// Absent, not a placeholder: zero pins render no DECISIONS section at all
// (an empty ledger is noise — matches the transcript card's empty rule).
func TestSidebarDecisionsModuleAbsentWhenEmpty(t *testing.T) {
	m := mouseTestModel()
	lines := m.renderContextModules(40, 60)
	plain := stripSidebar(lines)
	if strings.Contains(plain, "DECISIONS") {
		t.Fatalf("idle model rendered DECISIONS:\n%s", plain)
	}
}

// Malformed pin payloads fail closed upstream; the module renders nothing
// rather than a partial ledger.
func TestSidebarDecisionsModuleFailsClosedOnMalformedPins(t *testing.T) {
	m := mouseTestModel()
	m.sessionEvents = []sessions.Event{
		{Type: sessions.EventDecisionPinned, Payload: []byte("{not json")},
	}
	lines := m.renderContextModules(40, 60)
	plain := stripSidebar(lines)
	if strings.Contains(plain, "DECISIONS") {
		t.Fatalf("malformed pins must render no ledger:\n%s", plain)
	}
}

// The RUN module (frame gEVp1 S1) shows elapsed/tokens/stage while a run is
// live. Fresh sessions start in design mode (model.go:1069), so the stage
// label reads design · <activity> there.
func TestSidebarRunModuleProjectsLiveRun(t *testing.T) {
	base := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m := mouseTestModel()
	m.now = func() time.Time { return base.Add(5*time.Minute + 12*time.Second) }
	m.activeRunID = 7
	m.pending = true
	m.turnStartedAt = base

	lines := m.renderContextModules(40, 60)
	plain := stripSidebar(lines)
	if !strings.Contains(plain, "RUN") {
		t.Fatalf("live run missing the RUN module:\n%s", plain)
	}
	for _, want := range []string{"elapsed", "5m 12s", "stage", "design ·"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("RUN module missing %q:\n%s", want, plain)
		}
	}
}

// An executing (non-design) run labels the stage executing.
func TestSidebarRunModuleExecutingStage(t *testing.T) {
	base := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m := mouseTestModel()
	m.designMode = false
	m.now = func() time.Time { return base.Add(30 * time.Second) }
	m.activeRunID = 7
	m.pending = true
	m.turnStartedAt = base

	lines := m.renderContextModules(40, 60)
	plain := stripSidebar(lines)
	if !strings.Contains(plain, "executing") {
		t.Fatalf("executing run missing the executing stage:\n%s", plain)
	}
	if strings.Contains(plain, "design") {
		t.Fatalf("non-design run must not show a design stage:\n%s", plain)
	}
}

// Event-driven presence: an idle session with design mode OFF renders no RUN
// module. (Fresh sessions default designMode=true — the design surface is
// session state — so the probe must turn it off explicitly.)
func TestSidebarRunModuleAbsentWhenIdle(t *testing.T) {
	m := mouseTestModel()
	m.designMode = false
	lines := m.renderContextModules(40, 60)
	plain := stripSidebar(lines)
	if strings.Contains(plain, "RUN") {
		t.Fatalf("idle session rendered the RUN module:\n%s", plain)
	}
}

// Design mode keeps the module alive even between turns (the design surface
// is the session's state, not a transient), with the design stage label.
func TestSidebarRunModuleStaysInDesignMode(t *testing.T) {
	m := mouseTestModel()
	m.designMode = true
	m.pending = false
	lines := m.renderContextModules(40, 60)
	plain := stripSidebar(lines)
	if !strings.Contains(plain, "RUN") || !strings.Contains(plain, "design") {
		t.Fatalf("design session lost the RUN module:\n%s", plain)
	}
}
