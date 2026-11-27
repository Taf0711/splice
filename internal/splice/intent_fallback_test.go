package splice

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// TestRepairInputCarriesTheFullRequest pins audit item F6 for the repair
// input: a repair pass reads the full request, not the distilled intent.
func TestRepairInputCarriesTheFullRequest(t *testing.T) {
	plan := schemas.ExecutionPlan{RequestIntent: "distilled", Request: "full request " + strings.Repeat("x", 400) + " deep/zz_target.go"}
	input := repairStageInput("run-f6", "code_writer", plan, nil, nil, nil, nil)
	if input.Request != plan.Request {
		t.Fatalf("repair input request = %d bytes, want the plan's full request", len(input.Request))
	}
	if input.TaskText() != plan.Request {
		t.Fatal("repair input must read the full request as its task text")
	}
}

// TestOnlyTaskReadingStagesGetTheRequest pins audit item R4: the full request
// goes only to stages that read it. Other stage inputs keep the distilled
// intent, so their marshaled size and token estimate do not grow.
func TestOnlyTaskReadingStagesGetTheRequest(t *testing.T) {
	plan := schemas.ExecutionPlan{RequestIntent: "distilled", Request: "full request text"}
	for _, impl := range []stages.Stage{stages.CodeWriter{}, stages.TestGenerator{}, stages.PromptStage{Name: "node", Template: "t"}} {
		if got := taskRequestFor(impl, plan); got != plan.Request {
			t.Fatalf("%T input request = %q, want the full request", impl, got)
		}
	}
	for _, impl := range []stages.Stage{stages.TestRunner{}, &stages.StaticAnalyzer{}} {
		if got := taskRequestFor(impl, plan); got != "" {
			t.Fatalf("%T input carries the request %q; only task-reading stages may", impl, got)
		}
	}
}
