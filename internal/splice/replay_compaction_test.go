package splice

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// TestReplayGuard_CompactionNotConsumed (Test F): an item dropped by
// compactStageInput before the model sees it must NOT be marked consumed.
//
// Bracket (measured): three admitted observations serialize to ~460 tokens;
// the trivial-tier reserve is 500, so InputMax = -300 (allowance 200) forces
// compaction to drop exactly one observation (last-first: ID 3), delivering
// IDs 1 and 2. The relaxed second invocation must re-deliver 3 (never
// consumed) and suppress 1 and 2 (consumed).
func TestReplayGuard_CompactionNotConsumed(t *testing.T) {
	root := t.TempDir()
	var obs []schemas.MemoryObservation
	for i := int64(1); i <= 3; i++ {
		o := obsWithID(i, root, "content content content")
		o.ProjectPath = &root
		obs = append(obs, o)
	}
	intent := "task"
	store := &stubStore{bundle: schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations:    obs,
	}}
	tightPlan := schemas.ExecutionPlan{
		Tier:          schemas.TierTrivial,
		RequestIntent: intent,
		Stages: []schemas.ExecutionStage{
			{Name: "code_writer", Budget: schemas.StageBudget{InputMax: -250, OutputMax: 100}},
		},
	}
	tr := newRunTraceAccumulator(nil, "run-f", "session", root, tightPlan, "active", nil)
	got := prepareStageInputForTest(t, store, tightPlan, "code_writer", intent, root, 1, tr)
	if got.MemoryBundle == nil || len(got.MemoryBundle.Observations) != 2 {
		t.Fatalf("tight delivery = %v, want exactly two items (one compacted away)", idsOf(got.MemoryBundle))
	}
	survivorIDs := map[int64]bool{}
	for _, o := range got.MemoryBundle.Observations {
		survivorIDs[o.ID] = true
	}
	dropped := int64(3)
	if survivorIDs[3] {
		t.Fatalf("compaction must drop the LAST observation first; got %v", idsOf(got.MemoryBundle))
	}

	// Relax the budget in the SAME run: the compaction-dropped item must be
	// deliverable (never consumed); delivered items stay consumed.
	relaxedPlan := schemas.ExecutionPlan{
		Tier:          schemas.TierTrivial,
		RequestIntent: intent,
		Stages: []schemas.ExecutionStage{
			{Name: "code_writer", Budget: schemas.StageBudget{InputMax: 0, OutputMax: 100}},
		},
	}
	relaxed := prepareStageInputForTest(t, store, relaxedPlan, "code_writer", intent, root, 2, tr)
	relaxedIDs := map[int64]bool{}
	for _, o := range relaxed.MemoryBundle.Observations {
		relaxedIDs[o.ID] = true
	}
	if !relaxedIDs[dropped] {
		t.Fatalf("relaxed delivery = %v; dropped item %d must stay eligible after compaction", idsOf(relaxed.MemoryBundle), dropped)
	}
	for id := range survivorIDs {
		if relaxedIDs[id] {
			t.Fatalf("item %d was consumed and then re-delivered", id)
		}
	}
	if tr.replaySuppressedCount() != len(survivorIDs) {
		t.Fatalf("replay suppressed = %d, want %d", tr.replaySuppressedCount(), len(survivorIDs))
	}
}

func idsOf(bundle *schemas.MemoryBundle) []int64 {
	if bundle == nil {
		return nil
	}
	out := []int64{}
	for _, o := range bundle.Observations {
		out = append(out, o.ID)
	}
	return out
}

func prepareStageInputForTest(t *testing.T, store MemoryStore, plan schemas.ExecutionPlan, stage, intent, root string, iteration int, tr *runTraceAccumulator) schemas.HarnessStageInput {
	t.Helper()
	prepared, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput(stage, intent),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, stage),
		Tier:      plan.Tier,
		Iteration: iteration,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
		Trace:     tr,
	})
	if err != nil {
		t.Fatalf("prepareStageInput: %v", err)
	}
	return prepared
}
