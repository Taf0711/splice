package splice

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// A topology that renames the builtin code_writer and test_runner nodes must
// still get local repair, the writer summary for the classifier, and the
// repair-time classifier boundaries.
func TestRepairRoleNamesFollowRenamedBuiltinNodes(t *testing.T) {
	plan := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{
		{Name: "writer", Type: "code_writer"},
		{Name: "tests", Type: "test_runner"},
	}}
	if got := roleStageName(plan, "code_writer"); got != "writer" {
		t.Fatalf("code_writer role = %q, want writer", got)
	}
	if got := roleStageName(plan, "test_runner"); got != "tests" {
		t.Fatalf("test_runner role = %q, want tests", got)
	}
	legacy := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "code_writer"}}}
	if got := roleStageName(legacy, "code_writer"); got != "code_writer" {
		t.Fatalf("legacy plan role = %q, want code_writer", got)
	}

	passed := []schemas.StageRecord{{Name: "writer", OutputSummary: Ptr("wrote add.go")}}
	if got := passChange(passed, roleStageName(plan, "code_writer")); got != "wrote add.go" {
		t.Fatalf("passChange = %q, want the writer summary", got)
	}

	eval := &trajectoryEvaluator{label: "step_back"}
	advisor := classifierAdvisor(t, eval)
	advisor.config.QuestionPack = QuestionPackTrajActionV2
	options := PipelineRunConfig{Advisor: advisor}
	options.ClassifierPolicy = newHostTrajectoryPolicy(advisor, options.TrajectoryConfig, false)
	if options.ClassifierPolicy == nil {
		t.Fatal("classifier policy must be wired for the fixture")
	}
	options.ClassifierPolicy.history = []schemas.IterationState{
		{TestsFailing: 1, StateHash: "a", FilesChanged: []string{"a.go"}},
		{TestsFailing: 1, StateHash: "b", FilesChanged: []string{"b.go"}},
	}
	options.ClassifierPolicy.maxIterations = defaultMaxIterations
	options.ClassifierPolicy.tokenBudget = 10000
	var progress strings.Builder
	options.OnReasoning = func(s string) { progress.WriteString(s) }

	writer := &approachWriter{}
	runner := &freshFailureRunner{}
	registry := stageRegistry{"writer": writer, "tests": runner}
	var outputs []schemas.HarnessStageOutput
	var records []schemas.StageRecord
	priorSummaries := map[string]string{}
	priorChangedFiles := map[string][]string{}
	trace := newRunTraceAccumulator(nil, "repair-run", "session", "/project", plan, "off", nil)
	repaired, interaction, err := attemptLocalRepair(
		context.Background(), "repair-run", 3, plan, registry, nil,
		options, t.TempDir(), nil, nil, trace, nil,
		time.Now().Add(time.Minute), NewStageExecutionBudget(0), &records, &outputs,
		&priorSummaries, &priorChangedFiles, runner.current(),
	)
	if err != nil {
		t.Fatalf("repair with renamed nodes: %v", err)
	}
	if repaired || interaction == nil || interaction.Repairs != 2 || writer.calls != 2 {
		t.Fatalf("repaired=%v interaction=%+v writer calls=%d progress=%s", repaired, interaction, writer.calls, progress.String())
	}
	if n := len(advisor.Records()); n != 2 {
		t.Fatalf("repair-time classifier boundaries = %d, want 2", n)
	}
}

// A renamed test node still feeds verification capture and test-command memory.
// A renamed writer still gets its builtin prompt, its summary under the builtin
// key for test_generator, and its name on the classifier snapshot.
func TestRenamedRolesReachVerificationMemoryAndPrompts(t *testing.T) {
	plan := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{
		{Name: "writer", Type: "code_writer"},
		{Name: "tests", Type: "test_runner"},
	}}
	testStage := roleStageName(plan, "test_runner")
	result := schemas.PipelineResult{Stages: []schemas.StageRecord{
		{Name: "tests", Status: schemas.StageCompleted, TestCommand: "go test ./..."},
	}}
	if got := pipelineTestCommand(result, testStage); got != "go test ./..." {
		t.Fatalf("pipeline test command = %q", got)
	}
	if ver := captureVerificationFromResult(result, testStage); !ver.TestStageRan || ver.TestCommand != "go test ./..." {
		t.Fatalf("verification = %+v", ver)
	}

	out := schemas.HarnessStageOutput{Data: map[string]any{"test_command": []string{"go", "test", "./..."}}}
	obs := extractWriteObservations("tests", testStage, "run-rename", "/project", out)
	if len(obs) != 1 || obs[0].MemoryType != "test_command" {
		t.Fatalf("memory observations = %+v", obs)
	}

	if stages.StagePrompt(stageRole(plan.Stages[0])) == "" {
		t.Fatal("renamed writer lost its builtin prompt")
	}

	prior := withRoleAlias(map[string]string{"writer": "wrote add.go"}, plan, "code_writer")
	if prior["code_writer"] != "wrote add.go" || prior["writer"] != "wrote add.go" {
		t.Fatalf("prior summaries = %+v", prior)
	}
	unrenamed := map[string]string{"code_writer": "x"}
	if got := withRoleAlias(unrenamed, schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "code_writer"}}}, "code_writer"); len(got) != 1 {
		t.Fatalf("unrenamed plan must not alias: %+v", got)
	}

	b := fabricatedBoundary()
	b.WriterStage = roleStageName(plan, "code_writer")
	snap, err := TrajectorySemanticSnapshot(b, QuestionPackTrajActionV2)
	if err != nil || snap.Stage != "writer" {
		t.Fatalf("snapshot stage = %q, err = %v", snap.Stage, err)
	}
}

// The stage input names a renamed writer, and leaves it empty otherwise.
func TestWriterStageNameOnlyWhenRenamed(t *testing.T) {
	renamed := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "writer", Type: "code_writer"}}}
	if got := writerStageName(renamed); got != "writer" {
		t.Fatalf("renamed writer = %q", got)
	}
	builtin := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "code_writer", Type: "code_writer"}}}
	if got := writerStageName(builtin); got != "" {
		t.Fatalf("builtin writer = %q, want empty", got)
	}
}
