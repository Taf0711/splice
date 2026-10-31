package splice

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// TestEvidencePlanNotBuiltWhenSubstitutionOff is the regression for the
// production default. With the switch unset, prepareStageInput must not build
// an evidence plan (and therefore must not spend a validation read). With the
// switch on, the plan is built as before.
func TestEvidencePlanNotBuiltWhenSubstitutionOff(t *testing.T) {
	workDir, rev, body := prodEvidenceRepo(t)
	mem := prodEvidenceStore(t, workDir, rev, body)

	prepare := func(set flags.Set) *EvidencePlan {
		t.Helper()
		_, planScope, _, err := prepareStageInput(context.Background(), stageInputPreparation{
			Input: schemas.HarnessStageInput{
				RunID: "evidence-gate", StageName: "code_writer", Sequence: 1,
				PlanTier:      schemas.TierLight,
				RequestIntent: "Add RetentionDeficit in internal/audit/retention.go and reuse Apply.",
			},
			Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true, PullContext: true}},
			Budget:    schemas.StageBudget{InputMax: 20000, OutputMax: 8192},
			Tier:      schemas.TierLight,
			Iteration: 1,
			WorkDir:   workDir,
			Options:   PipelineConfigFromAgentOptions(agent.Options{Flags: set}),
			Memory:    mem,
		})
		if err != nil {
			t.Fatalf("prepareStageInput: %v", err)
		}
		return planScope.Evidence
	}

	if got := prepare(nil); got != nil {
		t.Fatalf("evidence plan built while substitution is off: %+v", got)
	}
	// Prefetch does not read the evidence plan. With prefetch on and
	// substitution off, an admitted substitution must not replace the
	// first-round request (V2.1: it did, on 41 of 60 memory reads).
	if got := prepare(flags.Set{flags.MemoryPrefetch: true}); got != nil {
		t.Fatalf("evidence plan built with prefetch on and substitution off: %+v", got)
	}

	got := prepare(flags.Set{flags.MemoryEvidenceSubstitution: true})
	if got == nil {
		t.Fatal("evidence plan not built while substitution is on")
	}
	if got.SubstitutionCount() == 0 {
		t.Fatalf("opt-in fixture admitted no substitution: %+v", got)
	}
}
