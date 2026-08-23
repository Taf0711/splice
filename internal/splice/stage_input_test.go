package splice

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// oversizedMemoryBundle returns a memory bundle whose payload dwarfs the
// light-tier code_writer allowance, with observations and exemplars ordered
// strongest-first so compaction must drop from the end.
func oversizedMemoryBundle() *schemas.MemoryBundle {
	big := strings.Repeat("memory payload line ", 4000) // ~80KB of optional content
	bundle := &schemas.MemoryBundle{RequestingAgent: "code_writer"}
	for i := 0; i < 6; i++ {
		bundle.Exemplars = append(bundle.Exemplars, schemas.Exemplar{
			RunID:   fmt.Sprintf("kept-run-%02d", i),
			Content: big,
		})
	}
	for i := 0; i < 4; i++ {
		bundle.Observations = append(bundle.Observations, schemas.MemoryObservation{
			ID:      int64(i + 1),
			Scope:   "project",
			Title:   fmt.Sprintf("obs %d", i),
			Content: big,
		})
	}
	return bundle
}

func compactTestInput() schemas.HarnessStageInput {
	return schemas.HarnessStageInput{
		RunID:         "run-1",
		StageName:     "code_writer",
		RequestIntent: "add a session list endpoint to the demo service",
		AcceptanceFacts: []schemas.AcceptanceFact{{
			Statement:                        "GET /sessions returns every held session",
			AutomatedVerification:            true,
			RecommendedAutomatedVerification: true,
		}},
		PipelineStages: []string{"code_writer", "static_analyzer"},
	}
}

// TestStageInputOverflowCompactsAndRecords pins behavior (1): an input over the
// stage allowance compacts deterministically, keeps required content intact,
// records what was dropped, and never aborts.
func TestStageInputOverflowCompactsAndRecords(t *testing.T) {
	input := compactTestInput()
	input.MemoryBundle = oversizedMemoryBundle()

	var notes []string
	got, compacted, err := compactStageInput("code_writer", stageBudgets(schemas.TierLight)["code_writer"], schemas.TierLight, input, func(msg string) {
		notes = append(notes, msg)
	})
	if err != nil {
		t.Fatalf("compaction must not fail while optional payload remains: %v", err)
	}
	if !compacted {
		t.Fatal("compacted = false, want true for an overflowing input")
	}
	if estimateStageInputTokens(got) > inputAllowanceTokens(stageBudgets(schemas.TierLight)["code_writer"], schemas.TierLight) {
		t.Fatalf("compacted input = %d tokens, still over allowance", estimateStageInputTokens(got))
	}
	if got.RequestIntent != input.RequestIntent || len(got.AcceptanceFacts) != len(input.AcceptanceFacts) {
		t.Fatal("required content was dropped or truncated")
	}
	if len(notes) == 0 {
		t.Fatal("no compaction notes recorded")
	}
	for _, note := range notes {
		if !strings.Contains(note, "input compact: code_writer") {
			t.Fatalf("note %q does not name the stage and action", note)
		}
	}
}

// TestStageInputOverflowWithoutOptionalPayloadFailsLoudly pins behavior (2):
// when even the stripped input exceeds the allowance, the caller gets a loud
// error naming both sizes. Required content is never silently truncated.
func TestStageInputOverflowWithoutOptionalPayloadFailsLoudly(t *testing.T) {
	input := compactTestInput()
	input.AcceptanceFacts = []schemas.AcceptanceFact{{Statement: strings.Repeat("required fact ", 20000)}}

	_, _, err := compactStageInput("code_writer", stageBudgets(schemas.TierLight)["code_writer"], schemas.TierLight, input, func(string) {})
	if err == nil {
		t.Fatal("expected a loud overflow error when required content alone exceeds the allowance")
	}
	for _, want := range []string{"code_writer", "input overflow", "never truncated"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

// TestCompactionIsDeterministic pins behavior (4): compacting the same input
// twice produces byte-identical results and identical notes.
func TestCompactionIsDeterministic(t *testing.T) {
	build := func() schemas.HarnessStageInput {
		input := compactTestInput()
		input.MemoryBundle = oversizedMemoryBundle()
		input.PriorSummaries = map[string]string{
			"test_runner":         strings.Repeat("prior verification ", 500),
			"static_analyzer":     strings.Repeat("prior findings ", 500),
			"acceptance_verifier": strings.Repeat("prior acceptance ", 500),
		}
		input.PriorChangedFiles = map[string][]string{"test_runner": {"session.go"}}
		ctx := "revision context"
		input.RevisionContext = &ctx
		return input
	}

	run := func() (string, []string) {
		var notes []string
		got, _, err := compactStageInput("code_writer", stageBudgets(schemas.TierLight)["code_writer"], schemas.TierLight, build(), func(msg string) {
			notes = append(notes, msg)
		})
		if err != nil {
			t.Fatalf("compact: %v", err)
		}
		encoded, mErr := json.Marshal(got)
		if mErr != nil {
			t.Fatalf("marshal: %v", mErr)
		}
		return string(encoded), notes
	}

	firstOut, firstNotes := run()
	secondOut, secondNotes := run()
	if firstOut != secondOut {
		t.Fatal("same input produced different compacted outputs")
	}
	if strings.Join(firstNotes, "|") != strings.Join(secondNotes, "|") {
		t.Fatalf("note sequences differ:\n%v\n%v", firstNotes, secondNotes)
	}
}

// TestOutputOverflowStillAborts pins behavior (3): output overflow stays fatal.
// The trajectory token-budget rule aborts on consumed >= budget regardless of
// whether the spend came from generation or input, so an output-only overrun
// must produce ActionAbortBudget.
func TestOutputOverflowStillAborts(t *testing.T) {
	budget := 100
	history := []schemas.IterationState{
		{Iteration: 1, TokensConsumed: budget},
	}
	decision := EvaluateTrajectory(history, 10, &budget)
	if decision.Action != schemas.ActionAbortBudget {
		t.Fatalf("decision action = %q, want %q at full budget consumption", decision.Action, schemas.ActionAbortBudget)
	}
}

// TestNoCompactionUnderAllowance pins the no-op path: an input under its
// allowance passes through untouched with no notes.
func TestNoCompactionUnderAllowance(t *testing.T) {
	input := compactTestInput()
	fired := false
	got, compacted, err := compactStageInput("code_writer", stageBudgets(schemas.TierLight)["code_writer"], schemas.TierLight, input, func(string) {
		fired = true
	})
	if err != nil || compacted || fired {
		t.Fatalf("under-allowance input must pass through untouched: (%v, %v, fired=%v)", err, compacted, fired)
	}
	if got.RunID != input.RunID || got.StageName != input.StageName {
		t.Fatal("pass-through mutated the input identity")
	}
}
