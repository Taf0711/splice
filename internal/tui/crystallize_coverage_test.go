package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestDesignCoverageWarningNamesEmptyOutOfScope(t *testing.T) {
	warning := designCoverageWarning(schemas.DesignPlan{})
	if !strings.Contains(warning, "out of scope") {
		t.Fatalf("warning = %q, want it to name out of scope", warning)
	}
}

func TestDesignCoverageWarningNamesTaskWithoutAcceptanceFacts(t *testing.T) {
	warning := designCoverageWarning(schemas.DesignPlan{
		Tasks: []schemas.Task{{Title: "Write parser"}},
	})
	if !strings.Contains(warning, `acceptance facts for task "Write parser"`) {
		t.Fatalf("warning = %q, want it to name the task without acceptance facts", warning)
	}
}

func TestDesignCoverageWarningNamesUnverifiedAcceptanceFact(t *testing.T) {
	warning := designCoverageWarning(schemas.DesignPlan{
		Tasks: []schemas.Task{{
			Title: "Write parser",
			AcceptanceFacts: []schemas.AcceptanceFact{{
				Statement: "The parser rejects invalid input.",
			}},
		}},
	})
	if !strings.Contains(warning, `acceptance fact "The parser rejects invalid input." on task "Write parser" has no automated verification command`) {
		t.Fatalf("warning = %q, want it to name the unverified acceptance fact", warning)
	}
}

func TestDesignCoverageWarningCompletePlanIsSilent(t *testing.T) {
	// A warning that always fires gets ignored, so a complete plan must be silent.
	command := "go test ./internal/tui"
	warning := designCoverageWarning(schemas.DesignPlan{
		OutOfScope:   []string{"the unrelated CLI"},
		SystemDesign: "The existing TUI renders the plan.",
		Tasks: []schemas.Task{{
			Title: "Write parser",
			AcceptanceFacts: []schemas.AcceptanceFact{{
				Statement:             "The parser accepts valid input.",
				AutomatedVerification: true,
				VerificationCommand:   &command,
			}},
		}},
	})
	if warning != "" {
		t.Fatalf("complete plan warning = %q, want no warning", warning)
	}
}

func TestCrystallizeCoverageWarningAppearsOnSuccessAndPartialFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "partial failure", err: errors.New("critic unavailable")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testSessionStore(t)
			m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
			sess, err := store.Create(sessions.CreateInput{SessionID: "coverage-" + strings.ReplaceAll(tt.name, " ", "-"), Cwd: t.TempDir()})
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			m.activeSession = sess
			m.activeRunID = 42
			critique := schemas.PlanCritique{OverallAssessment: "Looks good"}
			if tt.err != nil {
				critique = schemas.PlanCritique{}
			}
			updated, _ := m.Update(crystallizeResultMsg{
				runID:     42,
				plan:      coverageWarningPlan(),
				critique:  critique,
				err:       tt.err,
				store:     store,
				sessionID: sess.SessionID,
			})
			next := updated.(model)
			if !transcriptContains(next.transcript, "Design coverage note: the conversation did not settle these:") {
				t.Fatalf("transcript = %#v, want coverage warning", next.transcript)
			}
			if !transcriptContains(next.transcript, "Plan is ready.") {
				t.Fatalf("transcript = %#v, want plan-ready message", next.transcript)
			}
		})
	}
}

func TestCrystallizeCoverageWarningDoesNotBlockApproval(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	sess, err := store.Create(sessions.CreateInput{SessionID: "coverage-approval", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	m.activeRunID = 42
	updated, _ := m.Update(crystallizeResultMsg{
		runID:     42,
		plan:      coverageWarningPlan(),
		critique:  schemas.PlanCritique{OverallAssessment: "Looks good"},
		store:     store,
		sessionID: sess.SessionID,
	})
	next := updated.(model)
	if next.pendingPlan == nil {
		t.Fatal("coverage warning discarded the pending plan")
	}

	next.provider = nil
	approved, _ := next.handleApproveCommand()
	if transcriptContains(approved.transcript, "No pending plan") {
		t.Fatalf("approval was blocked before provider check: %#v", approved.transcript)
	}
	if !transcriptContains(approved.transcript, "No provider configured.") {
		t.Fatalf("approval did not reach provider check: %#v", approved.transcript)
	}
}

func coverageWarningPlan() schemas.DesignPlan {
	return schemas.DesignPlan{
		Epic:         "Build a parser",
		Requirements: []string{"The parser works."},
		InScope:      []string{"The parser package."},
		Tasks: []schemas.Task{{
			ID:     "parse",
			Title:  "Write parser",
			Intent: "Implement the parser.",
		}},
		Source: "conversation",
	}
}

// Real plans carry up to nine unsettled criteria whose statements run to a
// sentence each, which produced a note over a thousand characters long on one
// line. The note is read at a glance, so it names a few and counts the rest.
func TestDesignCoverageWarningCapsItsLength(t *testing.T) {
	tasks := make([]schemas.Task, 0, 9)
	for i := 0; i < 9; i++ {
		tasks = append(tasks, schemas.Task{Title: fmt.Sprintf("Task %d", i)})
	}
	warning := designCoverageWarning(schemas.DesignPlan{Tasks: tasks})
	if !strings.Contains(warning, "and 8 more") {
		t.Fatalf("warning = %q, want the overflow count", warning)
	}
	if len(warning) > 300 {
		t.Fatalf("warning is %d chars, want a note short enough to read at a glance", len(warning))
	}
}
