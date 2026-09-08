package splice

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// semanticContextStage: first call emits a context request and records the
// executed StageOptions (override request when the scope bridge ran, plus
// the RunTool narrowing signal); second call finishes the stage.
type semanticContextStage struct {
	calls       int
	executedReq *schemas.ContextRequest
	runToolSet  bool
}

func (*semanticContextStage) Capabilities() stages.Capabilities {
	return stages.Capabilities{PullContext: true}
}

func (s *semanticContextStage) Run(_ context.Context, _ schemas.HarnessStageInput, _ zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	s.calls++
	if s.calls == 1 {
		if options.OverrideContextRequest != nil {
			s.executedReq = options.OverrideContextRequest
		} else {
			def := stages.DefaultContextRequestFor("update the retry loop in main.go and the session store", options.WorkDir, options.Language)
			s.executedReq = &def
		}
		s.runToolSet = options.RunTool != nil
		return schemas.HarnessStageOutput{
			Summary:    "needs context",
			Confidence: 0.5,
			ContextRequest: &schemas.ContextRequest{
				Reason: "inspect",
				Queries: []schemas.ContextQuery{{
					QueryType:  schemas.ContextGetSymbol,
					Symbol:     Ptr("foo"),
					MaxResults: 5,
					MaxChars:   1000,
				}},
			},
		}, nil
	}
	return schemas.HarnessStageOutput{Summary: "context handled", Confidence: 1}, nil
}

func scopeOnForScopeRuntime(t *testing.T) {
	t.Helper()
	t.Setenv(scopeModeEnv, "")
}

func TestSemanticOnlyPlan_ReachesContextSwapAtCallSite(t *testing.T) {
	scopeOnForScopeRuntime(t)
	stage := &semanticContextStage{}
	prior := &StageScopePlan{
		SemanticResolved: true,
		KnownFiles:       []string{"internal/billing/dunning.go"},
		ExpansionBudget:  2,
	}
	_, err := runStageWithContext(context.Background(), schemas.HarnessStageInput{
		RunID:     "run-semantic-context",
		StageName: "context_stage",
	}, stage, 1, agent.ModelSelection{Provider: runFakeProvider{}}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), nil, nil, 0, nil, prior, 0)
	if err != nil {
		t.Fatalf("runStageWithContext: %v", err)
	}
	req := stage.executedReq
	if req == nil {
		t.Fatal("stage saw no executed context request")
	}
	if len(req.Queries) == 0 {
		t.Fatal("executed request has no queries")
	}
	// The hinted file must be read first (priority position).
	if req.Queries[0].QueryType != schemas.ContextReadFile || req.Queries[0].Path == nil || *req.Queries[0].Path != "internal/billing/dunning.go" {
		t.Fatalf("first executed query must read the hinted file, got %+v", req.Queries[0])
	}
	// Every default operation class must remain present: the global
	// listing survives a semantic-only plan.
	sawList := false
	for _, q := range req.Queries[1:] {
		if q.QueryType == schemas.ContextListFiles {
			sawList = true
		}
	}
	if !sawList {
		t.Fatalf("default global listing must survive semantic priority, got %+v", req.Queries)
	}
	// The semantic-only plan must NOT narrow the tool runner.
	if stage.runToolSet {
		t.Fatal("RunTool must be nil for a semantic-only plan (no narrowing)")
	}
}

func TestSemanticPriorityHintedDuplicate_MovesNotDuplicates(t *testing.T) {
	scopeOnForScopeRuntime(t)
	stage := &semanticContextStage{}
	prior := &StageScopePlan{
		SemanticResolved: true,
		// main.go is already read by the default request for an intent
		// that names it; the billing file is genuinely new.
		KnownFiles:      []string{"main.go", "internal/billing/dunning.go"},
		ExpansionBudget: 2,
	}
	_, err := runStageWithContext(context.Background(), schemas.HarnessStageInput{
		RunID:         "run-semantic-dedupe",
		StageName:     "context_stage",
		RequestIntent: "update the retry loop in main.go and the billing dunning path",
	}, stage, 1, agent.ModelSelection{Provider: runFakeProvider{}}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), nil, nil, 0, nil, prior, 0)
	if err != nil {
		t.Fatalf("runStageWithContext: %v", err)
	}
	req := stage.executedReq
	if req == nil {
		t.Fatal("stage saw no executed context request")
	}
	mainReads := 0
	firstMain := -1
	dunningPos := -1
	for i, q := range req.Queries {
		if q.QueryType != schemas.ContextReadFile || q.Path == nil {
			continue
		}
		switch *q.Path {
		case "main.go":
			mainReads++
			if firstMain == -1 {
				firstMain = i
			}
		case "internal/billing/dunning.go":
			dunningPos = i
		}
	}
	if mainReads != 1 {
		t.Fatalf("hinted duplicate read count = %d, want exactly 1 (move-not-duplicate): %+v", mainReads, req.Queries)
	}
	if firstMain != 0 {
		t.Fatalf("moved default read must sit in the priority position, got index %d: %+v", firstMain, req.Queries)
	}
	if dunningPos == -1 {
		t.Fatal("genuinely-new hinted file must appear as a new read")
	}
	// Both hinted reads sit in the priority block ahead of the remaining
	// default queries; order within the block follows the sorted hints.
	if dunningPos > 1 {
		t.Fatalf("priority reads must lead the request, got dunning at %d: %+v", dunningPos, req.Queries)
	}
}

func TestSemanticOnlyPlan_DoesNotNarrowToolRunner(t *testing.T) {
	scopeOnForScopeRuntime(t)
	stage := &semanticContextStage{}
	prior := &StageScopePlan{
		SemanticResolved: true,
		KnownFiles:       []string{"internal/billing/dunning.go"},
		ExpansionBudget:  2,
	}
	_, err := runStageWithContext(context.Background(), schemas.HarnessStageInput{
		RunID:     "run-semantic-narrowing",
		StageName: "context_stage",
	}, stage, 1, agent.ModelSelection{Provider: runFakeProvider{}}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), nil, nil, 0, nil, prior, 0)
	if err != nil {
		t.Fatalf("runStageWithContext: %v", err)
	}
	if stage.runToolSet {
		t.Fatal("narrowing must require CognitionResolved: semantic-only plan left RunTool set")
	}
}
