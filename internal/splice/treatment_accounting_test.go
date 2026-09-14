package splice

// Treatment-accounting pins for the live stage-input path.
//
// The earlier pins asserted that a default request's reads were structurally
// omitted, and that a narrower symbol outline counted as replacement work.
// Their producer, ScopedContextRequest, is removed: the live path authorizes
// no host omission and returns a zero ScopeSuppression. The pins now hold that
// zero contract instead of arithmetic that can no longer occur, and they cover
// repair re-entry, which is where granted privileges previously persisted.

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// TestStageInputAuthorizesNoHostOmission pins the live contract: delivering
// retained memory never lets the host omit a default operation. The abandoned
// cognition scoping path was the only producer of suppression counts, so every
// field stays zero on the normal pass and on repair re-entry.
func TestStageInputAuthorizesNoHostOmission(t *testing.T) {
	workDir := t.TempDir()
	store := &plainFakeStore{obs: []schemas.MemoryObservation{
		mkObs(1, "Session notes", "InvalidateSession clears the session store cache"),
	}}
	const intent = "fix InvalidateSession in the session store"
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: intent,
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	tr := newRunTraceAccumulator(nil, "run", "session", workDir, plan, "active", nil)
	input := schemas.HarnessStageInput{
		RunID:         "run",
		StageName:     "code_writer",
		Sequence:      1,
		PlanTier:      plan.Tier,
		RequestIntent: intent,
	}

	for _, reentry := range []bool{false, true} {
		_, scope, sup, err := prepareStageInput(context.Background(), stageInputPreparation{
			Input:         input,
			Stage:         &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
			Budget:        stageBudgetByName(plan, "code_writer"),
			Tier:          plan.Tier,
			Iteration:     1,
			WorkDir:       workDir,
			Options:       PipelineConfigFromAgentOptions(agent.Options{}),
			Memory:        store,
			Trace:         tr,
			NowUnix:       1_800_000_000,
			RepairReentry: reentry,
		})
		if err != nil {
			t.Fatalf("prepareStageInput(reentry=%v): %v", reentry, err)
		}
		if sup != (ScopeSuppression{}) {
			t.Fatalf("reentry=%v: the live path must authorize no host omission, got %+v", reentry, sup)
		}
		if scope.CognitionResolved {
			t.Fatalf("reentry=%v: memory delivery alone must not claim resolved cognition", reentry)
		}
	}
}
