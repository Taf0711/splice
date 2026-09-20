package splice

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// TestResolveEvidenceSubstitution pins the opt-in contract. Unset and "off"
// mean off; "on" means on; an invalid value fails loud so a misspelled
// experiment cannot silently measure the default (cold) path.
func TestResolveEvidenceSubstitution(t *testing.T) {
	t.Setenv(EvidenceSubstitutionEnvVar, "")
	if on, err := resolveEvidenceSubstitution(); err != nil || on {
		t.Fatalf("unset = %v, %v; want false, nil", on, err)
	}
	t.Setenv(EvidenceSubstitutionEnvVar, "off")
	if on, err := resolveEvidenceSubstitution(); err != nil || on {
		t.Fatalf("off = %v, %v; want false, nil", on, err)
	}
	t.Setenv(EvidenceSubstitutionEnvVar, "ON")
	if on, err := resolveEvidenceSubstitution(); err != nil || !on {
		t.Fatalf("ON = %v, %v; want true, nil", on, err)
	}
	t.Setenv(EvidenceSubstitutionEnvVar, "banana")
	if _, err := resolveEvidenceSubstitution(); err == nil {
		t.Fatal("invalid value must fail loud")
	}
}

// TestEvidencePlanNotBuiltWhenSubstitutionOff is the regression for the
// production default. With the switch unset, prepareStageInput must not build
// an evidence plan (and therefore must not spend a validation read). With the
// switch on, the plan is built as before.
func TestEvidencePlanNotBuiltWhenSubstitutionOff(t *testing.T) {
	t.Setenv(EvidenceSubstitutionEnvVar, "")
	workDir, rev, body := prodEvidenceRepo(t)
	mem := prodEvidenceStore(t, workDir, rev, body)

	prepare := func() *EvidencePlan {
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
			Options:   PipelineConfigFromAgentOptions(agent.Options{}),
			Memory:    mem,
		})
		if err != nil {
			t.Fatalf("prepareStageInput: %v", err)
		}
		return planScope.Evidence
	}

	if got := prepare(); got != nil {
		t.Fatalf("evidence plan built while substitution is off: %+v", got)
	}

	t.Setenv(EvidenceSubstitutionEnvVar, "on")
	got := prepare()
	if got == nil {
		t.Fatal("evidence plan not built while substitution is on")
	}
	if got.SubstitutionCount() == 0 {
		t.Fatalf("opt-in fixture admitted no substitution: %+v", got)
	}
}
