package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
)

// TestAskUserGateSignature pins the P3 gate elevation (§8): the blocking
// question card carries the [?] NEEDS YOU signature with the wait timer and
// the hard-gate invariant footer.
func TestAskUserGateSignature(t *testing.T) {
	questions := []agent.AskUserQuestion{{Question: "Buffer streamed bodies?"}}
	prompt := pendingAskUserPrompt{
		request: agent.AskUserRequest{
			Header:    "Retry policy",
			Questions: questions,
		},
		states:    newAskUserStates(questions),
		startedAt: time.Now().Add(-41 * time.Second),
	}
	plain := stripANSI(renderAskUserQuestionnaire(prompt, "", 100))
	for _, want := range []string{
		"[?]", "NEEDS YOU", "blocked 00:41",
		"-- no work running | no tokens burning --",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("gate card missing %q:\n%s", want, plain)
		}
	}
}

// TestAskUserGateTimer pins the timer contract: 41s renders as 00:41, and a
// zero start time (unknown) hides the timer while keeping the NEEDS YOU word.
func TestAskUserGateTimer(t *testing.T) {
	if got := formatGateWait(41 * time.Second); got != "00:41" {
		t.Fatalf("41s = %q, want 00:41", got)
	}
	if got := formatGateWait(4*time.Minute + 5*time.Second); got != "04:05" {
		t.Fatalf("4m05s = %q, want 04:05", got)
	}
	if got := formatGateWait(-time.Second); got != "00:00" {
		t.Fatalf("negative duration = %q, want 00:00", got)
	}
	// Zero start time: the timer hides, the word stays.
	prompt := pendingAskUserPrompt{
		request:   agent.AskUserRequest{Questions: []agent.AskUserQuestion{{Question: "q?"}}},
		states:    newAskUserStates([]agent.AskUserQuestion{{Question: "q?"}}),
		startedAt: time.Time{},
	}
	plain := stripANSI(renderAskUserQuestionnaire(prompt, "", 90))
	if !strings.Contains(plain, "NEEDS YOU") {
		t.Fatalf("gate card missing NEEDS YOU:\n%s", plain)
	}
	if strings.Contains(plain, "blocked ") {
		t.Fatalf("zero start time rendered a wait timer:\n%s", plain)
	}
}

// TestAskUserGateElevationBreaksPattern pins audit finding 4: the gate card
// is visually distinct from ordinary transcript rows — the elevation header
// and invariant footer are present, and the picker still works beneath the
// elevation.
func TestAskUserGateElevationBreaksPattern(t *testing.T) {
	prompt := pendingAskUserPrompt{
		request: agent.AskUserRequest{
			Header: "Retry policy",
			Questions: []agent.AskUserQuestion{{
				Question:    "Buffer streamed bodies?",
				Options:     []string{"buffer <= 1 MiB", "never retry"},
				Recommended: "never retry",
			}},
		},
		states:    newAskUserStates(nil),
		startedAt: time.Now().Add(-12 * time.Second),
	}
	prompt.states = newAskUserStates(prompt.request.Questions)
	plain := stripANSI(renderAskUserQuestionnaire(prompt, "", 90))
	// All three elevation channels present (glyph + word + invariant).
	for _, want := range []string{"[?]", "NEEDS YOU", "no work running", "no tokens burning"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("gate card missing %q:\n%s", want, plain)
		}
	}
	// The picker still works beneath the elevation: numbered options, the
	// recommended tag, and the type-your-own row survive.
	for _, want := range []string{"1. buffer", "2. never retry", "recommended", "Type your own answer"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("gate card lost picker affordance %q:\n%s", want, plain)
		}
	}
}

// TestAskUserGateWiredFromRun pins the runtime wiring: an askUserRequestMsg
// with a matching runID opens the gate with startedAt set, so the NEEDS YOU
// timer runs from the moment the orchestrator blocked.
func TestAskUserGateWiredFromRun(t *testing.T) {
	m := mouseTestModel()
	m.activeRunID = 7
	updated, _ := m.Update(askUserRequestMsg{
		runID: 7,
		request: agent.AskUserRequest{
			Questions: []agent.AskUserQuestion{{Question: "Buffer streamed bodies?"}},
		},
		answer: func([]string) {},
	})
	next := updated.(model)
	if next.pendingAskUser == nil {
		t.Fatal("gate did not open")
	}
	if next.pendingAskUser.startedAt.IsZero() {
		t.Fatal("gate opened without a start time; the NEEDS YOU timer cannot run")
	}
}
