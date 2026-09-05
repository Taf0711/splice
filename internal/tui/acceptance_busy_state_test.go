package tui

// acceptance_busy_state_test.go (P2 slice 2, §23): the busy-state guards are
// a shared contract — loops (loopBusy), queued prompts
// (launchQueuedMessageIfReady), and the commands that refuse to run mid-run
// (/new, /retry, /rewind) all claim to mirror each other. These probes pin
// the contract so the mirrors cannot drift apart silently again.

import (
	"testing"
)

// loopBusy and launchQueuedMessageIfReady claim to mirror: a state that
// blocks loops from firing must also block a queued prompt from launching,
// with one documented exception (a prompt queued before the modal opened may
// still deliver — modal guards in loopBusy exist for RUN LAUNCH, while
// launchQueuedMessageIfReady only guards gates that own the composer).
// Compaction, however, must block BOTH: compactResultMsg rewrites the
// transcript and session events wholesale, and a run launched mid-compaction
// would race that rewrite (the same race /retry already guards against).
func TestLaunchQueuedMessageWaitsForCompaction(t *testing.T) {
	m := mouseTestModel()
	m.provider = &fakeProvider{}
	m.queuedMessage = "fix the users service"
	m.compactInFlight = true

	next, cmd := m.launchQueuedMessageIfReady()
	if cmd != nil {
		t.Fatal("busy-state probe: queued prompt launched during compaction; it would race compactResultMsg's transcript rewrite")
	}
	if next.queuedMessage != "fix the users service" {
		t.Fatal("busy-state probe: queued message consumed during compaction and lost")
	}
}

// The busy-state predicate must agree with the command guards: /new refuses
// while compaction is in flight, so a queued prompt must not sneak a launch
// through either. This is the drift the mirrors had already accumulated.
func TestBusyPredicatesAgreeOnCompaction(t *testing.T) {
	m := mouseTestModel()
	m.compactInFlight = true
	if !m.loopBusy() {
		t.Fatal("busy-state probe: compaction does not block loops (loopBusy), but /new and /retry treat it as busy")
	}

	idle := mouseTestModel()
	if idle.loopBusy() {
		t.Fatal("busy-state probe: idle model reports busy")
	}
	if _, cmd := idle.launchQueuedMessageIfReady(); cmd != nil {
		t.Fatal("busy-state probe: idle model launched a message that was never queued")
	}
}
