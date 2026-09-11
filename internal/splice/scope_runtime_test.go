package splice

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// semanticContextStage: first call emits a context request and records the
// executed StageOptions (whether an override was installed, and whether the
// tool runner was narrowed); second call finishes the stage.
//
// The override flag matters after the cognition scoping path was abandoned:
// only an admitted evidence substitution may install an override, so a
// semantic-only plan must leave OverrideContextRequest nil.
type semanticContextStage struct {
	calls       int
	executedReq *schemas.ContextRequest
	sawOverride bool
	runToolSet  bool
	// listOutput is what the stage's list_directory call actually returned. It
	// is the real narrowing signal: RunTool is always non-nil when a runner is
	// supplied, so its nil-ness says nothing about whether the scope wrapped it.
	listOutput string
}

func (*semanticContextStage) Capabilities() stages.Capabilities {
	return stages.Capabilities{PullContext: true}
}

func (s *semanticContextStage) Run(_ context.Context, _ schemas.HarnessStageInput, _ zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	s.calls++
	if s.calls == 1 {
		s.sawOverride = options.OverrideContextRequest != nil
		if options.OverrideContextRequest != nil {
			s.executedReq = options.OverrideContextRequest
		} else {
			def := stages.DefaultContextRequestFor("update the retry loop in main.go and the session store", options.WorkDir, options.Language)
			s.executedReq = &def
		}
		s.runToolSet = options.RunTool != nil
		if options.RunTool != nil {
			res, err := options.RunTool(context.Background(), "list_directory", map[string]any{})
			if err != nil {
				return schemas.HarnessStageOutput{}, err
			}
			s.listOutput = res.Output
		}
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

// plainRunner is the inner runner. Its list_directory output is a fixed marker
// so a test can tell an unsuppressed listing from a cognition-scope directive.
func plainRunner() ToolRunner {
	return ToolRunnerFunc(func(_ context.Context, name string, _ map[string]any) (ToolResult, error) {
		if name == "list_directory" {
			return ToolResult{OK: true, Output: innerListing}, nil
		}
		return ToolResult{OK: true, Output: ""}, nil
	})
}

// innerListing is the marker the inner runner returns for a listing.
const innerListing = "INNER-LISTING"

func runSemanticCase(t *testing.T, prior *StageScopePlan, runID string, intent string) *semanticContextStage {
	t.Helper()
	stage := &semanticContextStage{}
	_, err := runStageWithContext(context.Background(), schemas.HarnessStageInput{
		RunID:         runID,
		StageName:     "context_stage",
		RequestIntent: intent,
	}, stage, 1, agent.ModelSelection{Provider: runFakeProvider{}}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), plainRunner(), nil, 0, nil, prior, 0)
	if err != nil {
		t.Fatalf("runStageWithContext: %v", err)
	}
	return stage
}

// TestSemanticOnlyPlan_NoContextOverrideAfterScopingAbandoned pins the new
// contract. The legacy cognition scoping path moved known files to the front of
// the host context request. That path is abandoned, so a semantic-only plan -
// which has no admitted substitution - must NOT install an override at all. The
// stage sees the deterministic default request.
func TestSemanticOnlyPlan_NoContextOverrideAfterScopingAbandoned(t *testing.T) {
	scopeOnForScopeRuntime(t)
	stage := runSemanticCase(t, &StageScopePlan{
		SemanticResolved: true,
		KnownFiles:       []string{"internal/billing/dunning.go"},
		ExpansionBudget:  2,
	}, "run-semantic-no-override", "")

	if stage.executedReq == nil {
		t.Fatal("stage saw no executed context request")
	}
	if stage.sawOverride {
		t.Fatalf("semantic-only plan installed a context override; the abandoned scoping path must not run: %+v", stage.executedReq.Queries)
	}
	// The default request leads with the workspace listing. The abandoned path
	// would have moved the hinted read to index 0.
	if len(stage.executedReq.Queries) == 0 {
		t.Fatal("executed request has no queries")
	}
	if stage.executedReq.Queries[0].QueryType != schemas.ContextListFiles {
		t.Fatalf("default request must lead with the listing, got %+v", stage.executedReq.Queries[0])
	}
	for _, q := range stage.executedReq.Queries {
		if q.QueryType == schemas.ContextReadFile && q.Path != nil && *q.Path == "internal/billing/dunning.go" {
			t.Fatalf("the abandoned path must not inject a hinted read: %+v", stage.executedReq.Queries)
		}
	}
}

// TestSemanticOnlyPlan_DoesNotNarrowToolRunner pins that tool-level listing
// suppression still requires CognitionResolved, and is unaffected by the
// scoping abandonment. It checks the real signal: the listing call's output.
// An earlier version asserted RunTool != nil, which was vacuous because the
// runner is always installed.
func TestSemanticOnlyPlan_DoesNotNarrowToolRunner(t *testing.T) {
	scopeOnForScopeRuntime(t)
	stage := runSemanticCase(t, &StageScopePlan{
		SemanticResolved: true,
		KnownFiles:       []string{"internal/billing/dunning.go"},
		ExpansionBudget:  2,
	}, "run-semantic-narrowing", "")

	if stage.listOutput != innerListing {
		t.Fatalf("semantic-only plan must not suppress the listing, got %q", stage.listOutput)
	}
}

// TestExactAnchorPlan_NoContextOverrideWithoutSubstitution pins the same
// contract for the stronger plan shape. Even a fully CognitionResolved plan no
// longer reshapes the host request by itself; only an admitted substitution
// may. The scope plan still governs tool-level listing suppression.
func TestExactAnchorPlan_NoContextOverrideWithoutSubstitution(t *testing.T) {
	scopeOnForScopeRuntime(t)
	stage := runSemanticCase(t, &StageScopePlan{
		CognitionResolved: true,
		KnownFiles:        []string{"internal/billing/dunning.go"},
		KnownSymbols:      []string{"internal/billing/dunning.go#Apply"},
		ExpansionBudget:   2,
	}, "run-exact-no-substitution", "")

	if stage.sawOverride {
		t.Fatalf("a resolved scope without an admitted substitution must not override the request: %+v", stage.executedReq.Queries)
	}
	// Tool-level narrowing is still expected for a resolved scope, and it is
	// observed through the listing output rather than RunTool nil-ness.
	if !strings.Contains(stage.listOutput, "suppressed by cognition scope") {
		t.Fatalf("a CognitionResolved plan must still suppress the listing, got %q", stage.listOutput)
	}
}
