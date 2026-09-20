package splice

// F11 control-separation pins and D2 accounting pins at the stage-input
// seam. Scope-off must leave prompt-memory delivery byte-identical to the
// scope-on path: SPLICE_SCOPE_MODE shapes context acquisition only. Failed
// context reads must be counted separately from delivered context.

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

func treatmentTestSetup(t *testing.T) (workDir string, store *stubStore, plan schemas.ExecutionPlan, tr *runTraceAccumulator, inputs *[]schemas.HarnessStageInput) {
	t.Helper()
	t.Setenv(scopeModeEnv, "off") // the pin runs under scope-off
	workDir = t.TempDir()
	store = &stubStore{bundle: schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations:    []schemas.MemoryObservation{obsWithID(7, workDir, "fresh billing evidence")},
	}}
	plan = schemas.ExecutionPlan{Tier: schemas.TierLight, RequestIntent: "i", Stages: []schemas.ExecutionStage{{Name: "code_writer"}}}
	tr = newRunTraceAccumulator(nil, "run", "session", workDir, plan, "active", nil)
	inputs = &[]schemas.HarnessStageInput{}
	return workDir, store, plan, tr, inputs
}

func treatmentPrepare(t *testing.T, workDir string, store *stubStore, plan schemas.ExecutionPlan, tr *runTraceAccumulator, inputs *[]schemas.HarnessStageInput) schemas.HarnessStageInput {
	t.Helper()
	prepared, _, _, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     schemas.HarnessStageInput{RunID: "run", StageName: "code_writer", Sequence: 1, PlanTier: plan.Tier, RequestIntent: "i"},
		Stage:     &capturingStage{inputs: inputs, caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, "code_writer"),
		Tier:      plan.Tier,
		Iteration: 1,
		WorkDir:   workDir,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
		Trace:     tr,
	})
	if err != nil {
		t.Fatalf("prepareStageInput: %v", err)
	}
	return prepared
}

// TestScopeOffKeepsMemoryDeliveryUnchanged is the F11 pin: with
// SPLICE_SCOPE_MODE=off, the composed stage input still carries the memory
// bundle (delivery-only treatment) and the trace records the delivered
// items. Scope-off never strips prompt memory.
func TestScopeOffKeepsMemoryDeliveryUnchanged(t *testing.T) {
	workDir, store, plan, tr, inputs := treatmentTestSetup(t)
	prepared := treatmentPrepare(t, workDir, store, plan, tr, inputs)
	if prepared.MemoryBundle == nil {
		t.Fatal("scope-off stripped the prompt memory bundle: scope-off gates context acquisition only")
	}
	if len(prepared.MemoryBundle.Observations) != 1 {
		t.Fatalf("delivered observations = %d, want 1", len(prepared.MemoryBundle.Observations))
	}
	meta := tr.stages[stageKeyFor("code_writer", 1, 0)]
	if meta.MemoryItems != 1 {
		t.Fatalf("trace MemoryItems = %d, want the delivered 1", meta.MemoryItems)
	}
	// The retrieval telemetry still records the lookup path.
	if meta.MemoryLookupMode == "" {
		t.Fatal("scope-off must still record the memory lookup mode")
	}
}

// TestScopeOnDeliversSameMemory pins the control: with the scope on, the
// same store and stage produce the same delivered bundle. Delivery is
// orthogonal to the scope knob in both directions.
func TestScopeOnDeliversSameMemory(t *testing.T) {
	t.Setenv(scopeModeEnv, "on")
	workDir := t.TempDir()
	store := &stubStore{bundle: schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations:    []schemas.MemoryObservation{obsWithID(7, workDir, "fresh billing evidence")},
	}}
	plan := schemas.ExecutionPlan{Tier: schemas.TierLight, RequestIntent: "i", Stages: []schemas.ExecutionStage{{Name: "code_writer"}}}
	tr := newRunTraceAccumulator(nil, "run", "session", workDir, plan, "active", nil)
	inputs := &[]schemas.HarnessStageInput{}
	prepared := treatmentPrepare(t, workDir, store, plan, tr, inputs)
	if prepared.MemoryBundle == nil || len(prepared.MemoryBundle.Observations) != 1 {
		t.Fatalf("scope-on delivery = %#v, want the same single observation", prepared.MemoryBundle)
	}
}

// TestFailedContextReadsCountedSeparately pins D2 test 6: a context query
// that executes but fails is recorded as a failure next to the delivered
// items, never as successfully delivered context.
func TestFailedContextReadsCountedSeparately(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{Tier: schemas.TierLight, RequestIntent: "i", Stages: []schemas.ExecutionStage{{Name: "code_writer"}}}
	tr := newRunTraceAccumulator(nil, "run", "session", workDir, plan, "active", nil)
	errMsg := "read failed: permission denied"
	tr.recordContext("code_writer", 1, schemas.ContextBundle{
		Items: []schemas.ContextItem{
			{Summary: "delivered content"},
			{Summary: "Context query failed.", Error: &errMsg},
		},
	})
	meta := tr.stages[stageKeyFor("code_writer", 1, 0)]
	if meta.ContextItems != 2 {
		t.Fatalf("ContextItems = %d, want 2 (both were executed)", meta.ContextItems)
	}
	if meta.ContextFailures != 1 {
		t.Fatalf("ContextFailures = %d, want 1", meta.ContextFailures)
	}
	if err := meta.Validate(); err != nil {
		t.Fatalf("InputMeta invalid: %v", err)
	}
}

// TestZeroContextFailuresRecorded pins that a clean fulfillment records a
// measured zero (the json tag has no omitempty), not an absent field.
func TestZeroContextFailuresRecorded(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{Tier: schemas.TierLight, RequestIntent: "i", Stages: []schemas.ExecutionStage{{Name: "code_writer"}}}
	tr := newRunTraceAccumulator(nil, "run", "session", workDir, plan, "active", nil)
	tr.recordContext("code_writer", 1, schemas.ContextBundle{Items: []schemas.ContextItem{{Summary: "ok"}}})
	meta := tr.stages[stageKeyFor("code_writer", 1, 0)]
	if meta.ContextFailures != 0 {
		t.Fatalf("ContextFailures = %d, want measured zero", meta.ContextFailures)
	}
	// Schema-level: a present zero must not be a negative.
	meta.ContextFailures = -1
	if err := meta.Validate(); err == nil {
		t.Fatal("negative ContextFailures must fail validation")
	}
}
