package tui

// Open-question render probes (§7.1, frame kAYHl): the resume card renders
// the open QUESTION TEXT and the launch DECISIONS module carries the open
// count — both from the SAME reconstructed state, never invented.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/sessions"
	splicerun "github.com/Taf0711/splice/internal/splice"
)

func openQuestionEventForTUI(seq int, question string) sessions.Event {
	raw, _ := json.Marshal(map[string]string{"question": question})
	return sessions.Event{Sequence: seq, Type: sessions.EventOpenQuestionRaised, Payload: raw}
}

// The launch DECISIONS module carries the open count (amber) beside the
// settled count when the session carries open questions.
func TestLaunchDecisionsModuleShowsOpenCount(t *testing.T) {
	m := mouseTestModel()
	m.sessionEvents = []sessions.Event{
		{Type: sessions.EventDecisionPinned, Payload: p14DecisionPin(t, "retry idempotent methods", false)},
		openQuestionEventForTUI(2, "are streamed bodies idempotent?"),
	}
	lines := m.launchDecisionsModule(40)
	plain := strings.Join(stripANSIStrings(lines), "\n")
	if !strings.Contains(plain, "1 settled") {
		t.Fatalf("DECISIONS module missing the settled count:\n%s", plain)
	}
	if !strings.Contains(plain, "1 open") {
		t.Fatalf("DECISIONS module missing the open count:\n%s", plain)
	}
}

// No open questions: no open row (honest absence, never a padded zero).
func TestLaunchDecisionsModuleOmitsOpenWhenNone(t *testing.T) {
	m := mouseTestModel()
	m.sessionEvents = []sessions.Event{
		{Type: sessions.EventDecisionPinned, Payload: p14DecisionPin(t, "retry idempotent methods", false)},
	}
	lines := m.launchDecisionsModule(40)
	plain := strings.Join(stripANSIStrings(lines), "\n")
	if strings.Contains(plain, "open") {
		t.Fatalf("DECISIONS module invented an open row:\n%s", plain)
	}
}

// Open questions alone (no settled pins) still wake the module.
func TestLaunchDecisionsModuleWakeOnOpenOnly(t *testing.T) {
	m := mouseTestModel()
	m.sessionEvents = []sessions.Event{
		openQuestionEventForTUI(1, "do worktrees share the go cache?"),
	}
	lines := m.launchDecisionsModule(40)
	plain := strings.Join(stripANSIStrings(lines), "\n")
	if !strings.Contains(plain, "DECISIONS") || !strings.Contains(plain, "1 open") {
		t.Fatalf("open-only session did not wake the DECISIONS module:\n%s", plain)
	}
	if strings.Contains(plain, "settled") {
		t.Fatalf("open-only session invented a settled count:\n%s", plain)
	}
}

// The resume card renders the open QUESTION TEXT (frame: "[ ] 1 open
// are streamed bodies idempotent?") from the reconstructed state of a real
// session store — the full persistence round trip.
func TestLaunchResumeCardCarriesOpenQuestionText(t *testing.T) {
	store := testSessionStore(t)
	created, err := store.Create(sessions.CreateInput{Title: "design session", Cwd: "/tmp/oq-probe"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	raise, err := splicerun.OpenQuestionRaisedAppender("are streamed bodies idempotent?", "blocks the retry decision")
	if err != nil {
		t.Fatalf("raise appender: %v", err)
	}
	pin := splicerun.DecisionPinnedAppender("retry only idempotent methods", "", "")
	if _, err := store.AppendEvents(created.SessionID, []sessions.AppendEventInput{pin, raise}); err != nil {
		t.Fatalf("append: %v", err)
	}

	m := launchTestModel(t)
	m.cwd = "/tmp/oq-probe"
	m.sessionStore = store
	card := m.launchResumeCard()
	plain := strings.Join(stripANSIStrings(card), "\n")
	if !strings.Contains(plain, "[+]") || !strings.Contains(plain, "1 decisions settled") {
		t.Fatalf("resume card missing the settled row:\n%s", plain)
	}
	if !strings.Contains(plain, "[ ]") || !strings.Contains(plain, "1 open") {
		t.Fatalf("resume card missing the open row:\n%s", plain)
	}
	if !strings.Contains(plain, "are streamed bodies idempotent?") {
		t.Fatalf("resume card missing the question TEXT:\n%s", plain)
	}
}

// A resolved question does not render: the card shows the open set, not the
// audit trail.
func TestLaunchResumeCardHidesResolvedQuestions(t *testing.T) {
	store := testSessionStore(t)
	created, err := store.Create(sessions.CreateInput{Title: "design session", Cwd: "/tmp/oq-resolved"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	raise, _ := splicerun.OpenQuestionRaisedAppender("answered already?", "")
	resolve, err := splicerun.OpenQuestionResolvedAppender("answered already?", "settled")
	if err != nil {
		t.Fatalf("resolve appender: %v", err)
	}
	if _, err := store.AppendEvents(created.SessionID, []sessions.AppendEventInput{raise, resolve}); err != nil {
		t.Fatalf("append: %v", err)
	}

	m := launchTestModel(t)
	m.cwd = "/tmp/oq-resolved"
	m.sessionStore = store
	card := m.launchResumeCard()
	plain := strings.Join(stripANSIStrings(card), "\n")
	if strings.Contains(plain, "answered already?") || strings.Contains(plain, "1 open") {
		t.Fatalf("resume card rendered a resolved question:\n%s", plain)
	}
}

// A question longer than the card budget truncates with an ellipsis instead
// of pushing the card wide.
func TestTruncateOpenQuestion(t *testing.T) {
	long := strings.Repeat("x", 100)
	got := truncateOpenQuestion(long)
	if len([]rune(got)) > 57 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncateOpenQuestion = %d runes, want <=57 with an ellipsis", len([]rune(got)))
	}
	short := "short?"
	if again := truncateOpenQuestion(short); again != short {
		t.Fatalf("truncateOpenQuestion(%q) = %q, want unchanged", short, again)
	}
}
