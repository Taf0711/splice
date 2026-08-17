package splice

import (
	"context"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// TestMemoryIdentityUsesProjectRoot pins MD1: memory queries and observations
// key on the stable repo root (options.ProjectRoot) while tool execution keeps
// operating in the per-run worktree path.
func TestMemoryIdentityUsesProjectRoot(t *testing.T) {
	repoRoot := "/repo/root"
	worktreePaths := []string{
		"/repo/root/.splice/wt-a",
		"/repo/root/.splice/wt-b",
	}

	for _, workDir := range worktreePaths {
		plan := schemas.ExecutionPlan{
			Tier:          schemas.TierLight,
			RequestIntent: "add a function",
			Stages: []schemas.ExecutionStage{
				{Name: "code_writer"},
				{Name: "test_runner"},
			},
		}
		var upserts []schemas.MemoryObservation
		store := &stubStore{upserts: &upserts}
		capturer := &stageCallCapturer{calls: map[string]capturedStageCall{}, caps: stages.Capabilities{ConsumesMemory: true}}
		registry := stageRegistry{
			"code_writer": capturer,
			"test_runner": outputStage{output: schemas.HarnessStageOutput{
				Summary:    "tests pass",
				Confidence: 1,
				Data:       map[string]any{"test_command": []string{"go", "test", "./..."}},
			}},
		}
		options := PipelineConfigFromAgentOptions(agent.Options{})
		options.ProjectRoot = repoRoot

		_, _, completed, err := runPass(context.Background(), "run-id", 1, plan, registry, runFakeProvider{}, options, workDir, nil, time.Time{}, nil, store)
		if err != nil || !completed {
			t.Fatalf("workDir %q: runPass completed=%v err=%v", workDir, completed, err)
		}

		// The memory query keys on the repo root, not the worktree path.
		if len(store.queries) != 1 {
			t.Fatalf("workDir %q: queries = %d, want 1", workDir, len(store.queries))
		}
		if store.queries[0].ProjectPath == nil || *store.queries[0].ProjectPath != repoRoot {
			t.Fatalf("workDir %q: query ProjectPath = %#v, want %q", workDir, store.queries[0].ProjectPath, repoRoot)
		}

		// Write observations key on the repo root too.
		if len(upserts) != 1 {
			t.Fatalf("workDir %q: upserts = %d, want 1", workDir, len(upserts))
		}
		if upserts[0].ProjectPath == nil || *upserts[0].ProjectPath != repoRoot {
			t.Fatalf("workDir %q: observation ProjectPath = %#v, want %q", workDir, upserts[0].ProjectPath, repoRoot)
		}

		// Tool execution still runs in the worktree path.
		call, ok := capturer.calls["code_writer"]
		if !ok {
			t.Fatalf("workDir %q: code_writer stage did not run", workDir)
		}
		if call.options.WorkDir != workDir {
			t.Fatalf("workDir %q: stage WorkDir = %q, want the worktree path", workDir, call.options.WorkDir)
		}
	}
}

// TestMemoryIdentityFallbackUsesWorkDir pins the fallback: an empty ProjectRoot
// keeps today's behavior, where ProjectPath is the (absolute) working dir.
func TestMemoryIdentityFallbackUsesWorkDir(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "add a function",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	store := &stubStore{}
	registry := stageRegistry{
		"code_writer": &capturingStage{inputs: &[]schemas.HarnessStageInput{}, caps: stages.Capabilities{ConsumesMemory: true}},
	}
	// ProjectRoot is deliberately empty here.
	options := PipelineConfigFromAgentOptions(agent.Options{})

	_, _, completed, err := runPass(context.Background(), "run-id", 1, plan, registry, runFakeProvider{}, options, workDir, nil, time.Time{}, nil, store)
	if err != nil || !completed {
		t.Fatalf("runPass completed=%v err=%v", completed, err)
	}
	if len(store.queries) != 1 || store.queries[0].ProjectPath == nil || *store.queries[0].ProjectPath != workDir {
		t.Fatalf("query ProjectPath = %#v, want %q", store.queries[0].ProjectPath, workDir)
	}
}

// TestMemoryIdentityDegradationObservationUsesProjectRoot covers the third
// memory call site: context-degradation observations.
func TestMemoryIdentityDegradationObservationUsesProjectRoot(t *testing.T) {
	repoRoot := "/repo/root"
	workDir := "/repo/root/.splice/wt-a"
	var inputs []schemas.HarnessStageInput
	var upserts []schemas.MemoryObservation
	store := &stubStore{upserts: &upserts}
	stage := &contextRequestStage{inputs: &inputs}
	options := PipelineConfigFromAgentOptions(agent.Options{})
	options.ProjectRoot = repoRoot
	selection := agent.ModelSelection{Provider: runFakeProvider{}, ProviderName: "provider-a", Model: "model-a"}

	_, err := runStageWithContext(context.Background(), schemas.HarnessStageInput{
		RunID:     "run-degraded",
		StageName: "context_stage",
	}, stage, 1, selection, options, workDir, nil, store, 0)
	if err != nil {
		t.Fatalf("runStageWithContext: %v", err)
	}
	if len(upserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(upserts))
	}
	if upserts[0].ProjectPath == nil || *upserts[0].ProjectPath != repoRoot {
		t.Fatalf("degradation ProjectPath = %#v, want %q", upserts[0].ProjectPath, repoRoot)
	}
}

func TestMemoryProjectRootHelper(t *testing.T) {
	if got := memoryProjectRoot(PipelineRunConfig{}, "/abs/work"); got != "/abs/work" {
		t.Fatalf("empty ProjectRoot = %q, want workDir /abs/work", got)
	}
	if got := memoryProjectRoot(PipelineRunConfig{ProjectRoot: "/repo/root"}, "/abs/work"); got != "/repo/root" {
		t.Fatalf("set ProjectRoot = %q, want /repo/root", got)
	}
}

func TestBuildConfigObservationUsesProjectRoot(t *testing.T) {
	plan := schemas.ExecutionPlan{
		Tier: schemas.TierLight,
		Stages: []schemas.ExecutionStage{
			{Name: "code_writer"},
			{Name: "test_runner"},
		},
	}
	obs := buildConfigObservation("run-1", "/repo/root", plan)
	if obs.ProjectPath == nil || *obs.ProjectPath != "/repo/root" {
		t.Fatalf("config ProjectPath = %#v, want /repo/root", obs.ProjectPath)
	}
}
