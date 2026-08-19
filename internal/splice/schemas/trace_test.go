package schemas

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testBudget() StageBudget { return StageBudget{InputMax: 100, OutputMax: 100} }

func testPlan() *ExecutionPlan {
	return &ExecutionPlan{
		Tier:          TierLight,
		RequestIntent: "add a Hello function",
		Stages:        []ExecutionStage{{Name: "code_writer", Budget: testBudget()}, {Name: "test_runner", Budget: testBudget()}},
		TokenBudget:   TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, PerStage: map[string]StageBudget{"code_writer": testBudget(), "test_runner": testBudget()}, OverflowPolicy: "abort"},
	}
}

func testIterationState() IterationState {
	return IterationState{
		Iteration:      1,
		Timestamp:      1.0,
		TestsPassing:   1,
		StateHash:      "abc",
		Confidence:     0.9,
		Preexisting:    TestCounts{Pass: 1},
		Authored:       TestCounts{Pass: 0},
		TokensConsumed: 10,
	}
}

func testStageRecord() StageRecord {
	return StageRecord{Name: "code_writer", Status: StageCompleted, Iteration: 1}
}

func testRunOutcome() RunOutcome {
	return RunOutcome{
		SchemaVersion: TraceSchemaVersion,
		RunID:         "run-1",
		SessionID:     "sess-1",
		RepoRoot:      "/repo",
		Intent:        "add a Hello function",
		Tier:          string(TierLight),
		Plan:          testPlan(),
		Iterations:    []IterationState{testIterationState()},
		Stages:        []TracedStage{{StageRecord: testStageRecord(), InputMeta: InputMeta{MemoryItems: 1, MemoryChars: 10, EdgePayloadBytes: 42}}},
		Outcome:       OutcomeRecord{Status: "completed"},
		Memory:        MemoryRecord{Status: "active", Items: 1, Chars: 10},
		Interventions: []InterventionRecord{{Type: InterventionPermissionTap, Weight: 1, Stage: "code_writer", Iteration: 1, Summary: "bash: run tests", Choice: "allow"}},
	}
}

func TestRunOutcomeValidateRoundTrip(t *testing.T) {
	if err := testRunOutcome().Validate(); err != nil {
		t.Fatalf("valid run outcome failed Validate: %v", err)
	}
}

func TestOutcomeRecordAcceptsRunning(t *testing.T) {
	// Incremental trace writes use status "running"; it must validate as a
	// valid in-progress state alongside the settled states.
	for _, status := range []string{"running", "completed", "aborted", "failed"} {
		if err := (OutcomeRecord{Status: status}).Validate(); err != nil {
			t.Fatalf("status %q rejected: %v", status, err)
		}
	}
	if err := (OutcomeRecord{Status: "weird"}).Validate(); err == nil {
		t.Fatal("unknown status accepted")
	}
}

func TestRunOutcomeValidateRejectsInvalid(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*RunOutcome)
		wantErr string
	}{
		{"missing run_id", func(r *RunOutcome) { r.RunID = "" }, "run_id"},
		{"missing repo_root", func(r *RunOutcome) { r.RepoRoot = "" }, "repo_root"},
		{"missing intent", func(r *RunOutcome) { r.Intent = "" }, "intent"},
		{"nil plan", func(r *RunOutcome) { r.Plan = nil }, "plan"},
		{"bad schema version", func(r *RunOutcome) { r.SchemaVersion = "2" }, "schema_version"},
		{"bad outcome status", func(r *RunOutcome) { r.Outcome.Status = "weird" }, "outcome"},
		{"bad memory status", func(r *RunOutcome) { r.Memory.Status = "weird" }, "memory"},
		{"bad intervention weight", func(r *RunOutcome) { r.Interventions[0].Weight = 3 }, "weight"},
		{"bad iteration", func(r *RunOutcome) { r.Iterations[0].StateHash = "" }, "state_hash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ro := testRunOutcome()
			tc.mutate(&ro)
			if err := ro.Validate(); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestVerdictRecordValidate(t *testing.T) {
	valid := VerdictRecord{RunID: "run-1", Verdict: VerdictKept, DecidedAt: time.Now()}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid verdict failed: %v", err)
	}
	rejected := VerdictRecord{RunID: "run-1", Verdict: VerdictRejected, RejectReason: "wrong_approach", DecidedAt: time.Now()}
	if err := rejected.Validate(); err != nil {
		t.Fatalf("rejected verdict failed: %v", err)
	}
	if err := (VerdictRecord{RunID: "run-1", Verdict: "unknown", DecidedAt: time.Now()}).Validate(); err == nil {
		t.Fatal("unknown verdict must fail Validate")
	}
	if err := (VerdictRecord{RunID: "run-1", Verdict: VerdictKept}).Validate(); err == nil {
		t.Fatal("missing decided_at must fail Validate")
	}
}

// TestTraceDecodeToleratesUnknownFields pins additive evolution: a payload
// with fields this schema version does not know still decodes cleanly, so a
// newer writer never breaks an older reader.
func TestTraceDecodeToleratesUnknownFields(t *testing.T) {
	base, err := json.Marshal(testRunOutcome())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var withUnknown map[string]any
	if err := json.Unmarshal(base, &withUnknown); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	withUnknown["research"] = []any{map[string]any{"query": "q"}}
	withUnknown["future_field"] = "present"
	extra, err := json.Marshal(withUnknown)
	if err != nil {
		t.Fatalf("marshal extra: %v", err)
	}
	var decoded RunOutcome
	if err := json.Unmarshal(extra, &decoded); err != nil {
		t.Fatalf("decode with unknown fields: %v", err)
	}
	if decoded.RunID != "run-1" {
		t.Fatalf("run_id = %q, want run-1", decoded.RunID)
	}
}

// TestIterationStateTestCountsValidate pins the Q2 split validation.
func TestIterationStateTestCountsValidate(t *testing.T) {
	st := testIterationState()
	st.Preexisting.Fail = -1
	if err := st.Validate(); err == nil || !strings.Contains(err.Error(), "preexisting") {
		t.Fatalf("negative preexisting fail must be rejected, got %v", err)
	}
}
