package splice

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestTrajV1QuestionsAtRepairBoundary(t *testing.T) {
	eval := &recordingEvaluator{p: map[string]float64{"same_cause": 0.9, "new_information": 0.1}}
	cfg := shadowConfig()
	cfg.QuestionPack = QuestionPackTrajV1
	advisor, err := NewDecisionAdvisor(cfg, eval)
	if err != nil {
		t.Fatal(err)
	}
	boundary := fabricatedBoundary()
	boundary.Repair = 1
	boundary.Previous = &TrajectoryStep{Change: "Add the event.", Failure: "The event has no route."}
	boundary.Latest = &TrajectoryStep{Change: "Add the route.", Failure: "The audit trail cannot show the event."}
	classification, err := (advisorClassifier{advisor: advisor, pack: noulTrajectoryPack{}, budget: trajectoryRequestBudget}).Classify(context.Background(), boundary)
	if err != nil {
		t.Fatal(err)
	}
	if len(eval.last.Questions) != 2 || classification.Probabilities[LabelStepBack] != .81 {
		t.Fatalf("questions=%+v classification=%+v", eval.last.Questions, classification)
	}
	if !strings.Contains(eval.last.State, boundary.Previous.Failure) || !strings.Contains(eval.last.State, boundary.Latest.Failure) {
		t.Fatalf("state lacks the boundary evidence: %s", eval.last.State)
	}
}

func TestPassFailureKindUsesTypedEvidence(t *testing.T) {
	failedTests := schemas.HarnessStageOutput{Data: map[string]any{
		"test_results": schemas.TestRunResults{Tests: []schemas.TestCaseResult{{Name: "fails", Status: "failed"}}},
	}}
	transportError := "stream error: connection refused"
	cases := []struct {
		name    string
		records []schemas.StageRecord
		outputs []schemas.HarnessStageOutput
		want    string
	}{
		{
			name:    "typed stage error",
			records: []schemas.StageRecord{{Status: schemas.StageFailed, FailureKind: schemas.NodeFailureStageError}},
			want:    schemas.NodeFailureStageError,
		},
		{
			name:    "untyped transport failure defaults to stage error",
			records: []schemas.StageRecord{{Status: schemas.StageFailed, OutputSummary: &transportError}},
			want:    schemas.NodeFailureStageError,
		},
		{
			name:    "failing tests are a test failure",
			records: []schemas.StageRecord{{Status: schemas.StageCompleted}},
			outputs: []schemas.HarnessStageOutput{failedTests},
			want:    schemas.NodeFailureTestFailure,
		},
		{
			// P3-6: failing tests win over a failed stage in the same pass.
			name:    "failing tests before a failed stage",
			records: []schemas.StageRecord{{Status: schemas.StageFailed, FailureKind: schemas.NodeFailureStageError}},
			outputs: []schemas.HarnessStageOutput{failedTests},
			want:    schemas.NodeFailureTestFailure,
		},
		{
			name: "no evidence is none",
			want: schemas.NodeFailureNone,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := passFailureKind(tc.records, tc.outputs); got != tc.want {
				t.Fatalf("passFailureKind = %q, want %q", got, tc.want)
			}
		})
	}
}
