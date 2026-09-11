package splice

import (
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// TestEffectiveCapsTopologyWins pins that a compiled node overrides the
// stage's own declaration while runtime-only fields stay with the stage.
func TestEffectiveCapsTopologyWins(t *testing.T) {
	runtime := stages.Capabilities{ModelFree: false, PullContext: true, ConsumesMemory: true, Description: "runtime description"}
	yes, no := true, false
	stage := schemas.ExecutionStage{
		Name: "gen",
		Caps: &schemas.NodeCapabilities{ModelFree: yes, PullContext: &no, PullMemory: &no},
	}
	got := effectiveCaps(stage, runtime)
	if !got.ModelFree {
		t.Fatalf("ModelFree = false, want true from the node")
	}
	if got.PullContext {
		t.Fatalf("PullContext = true, want false from the node")
	}
	if got.ConsumesMemory {
		t.Fatalf("ConsumesMemory = true, want false from the node")
	}
	if got.Description != "runtime description" {
		t.Fatalf("Description = %q, want the runtime description", got.Description)
	}
}

// TestEffectiveCapsLegacyPlanKeepsRuntime pins the fallback: a plan with no
// compiled caps leaves the stage declaration untouched.
func TestEffectiveCapsLegacyPlanKeepsRuntime(t *testing.T) {
	runtime := stages.Capabilities{ModelFree: true, PullContext: false, ConsumesMemory: true, Description: "lint"}
	stage := schemas.ExecutionStage{Name: "lint"}
	if got := effectiveCaps(stage, runtime); got != runtime {
		t.Fatalf("effectiveCaps(no caps) = %+v, want the runtime caps", got)
	}
}

// TestStageModelFreeFallsBackToBuiltinProfile pins the preflight path: a
// legacy plan resolves model-free from the builtin profile, and compiled caps
// win when present.
func TestStageModelFreeFallsBackToBuiltinProfile(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"code_writer", false},
		{"test_generator", false},
		{"test_runner", true},
		{"static_analyzer", true},
		{"security_auditor", true},
		{"acceptance_verifier", true},
		{"unknown_stage", false},
	}
	for _, tc := range cases {
		if got := stageModelFree(schemas.ExecutionStage{Name: tc.name}); got != tc.want {
			t.Fatalf("stageModelFree(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
	free := true
	if got := stageModelFree(schemas.ExecutionStage{Name: "code_writer", Caps: &schemas.NodeCapabilities{ModelFree: free}}); !got {
		t.Fatal("stageModelFree with compiled caps = false, want true")
	}
}

// TestCompileTopologyCarriesResolvedCaps pins that every compiled stage
// carries a complete capability set, so the executor never falls back for a
// topology-compiled plan.
func TestCompileTopologyCarriesResolvedCaps(t *testing.T) {
	compiled, err := CompileTopology(defaultTopology(), schemas.TierStandard)
	if err != nil {
		t.Fatalf("CompileTopology = %v", err)
	}
	byName := make(map[string]schemas.ExecutionStage, len(compiled.Stages))
	for _, stage := range compiled.Stages {
		if stage.Caps == nil {
			t.Fatalf("stage %s has no compiled caps", stage.Name)
		}
		if stage.Caps.PullContext == nil || stage.Caps.PullMemory == nil {
			t.Fatalf("stage %s has unresolved capability pointers: %+v", stage.Name, *stage.Caps)
		}
		byName[stage.Name] = stage
	}
	if byName["code_writer"].Caps.ModelFree {
		t.Fatal("code_writer must be model-backed")
	}
	if byName["test_generator"].Caps.PullContext == nil || !*byName["test_generator"].Caps.PullContext {
		t.Fatal("test_generator must pull context")
	}
	runner := byName["test_runner"]
	if !runner.Caps.ModelFree || runner.Caps.PullContext == nil || *runner.Caps.PullContext ||
		runner.Caps.PullMemory == nil || *runner.Caps.PullMemory || !runner.Caps.ProducesVerification {
		t.Fatalf("test_runner caps = %+v, want model-free, no context, no memory, verification", *runner.Caps)
	}
}

// TestBuiltinCapabilityProfileMatchesStageDeclarations is the pairing guard for
// T4a: the compiled profile is authoritative for model-free, pull-context, and
// memory gating, so any drift from the stage implementation silently changes
// runtime behavior. This fails CI when one side moves without the other.
func TestBuiltinCapabilityProfileMatchesStageDeclarations(t *testing.T) {
	registry, err := buildStageRegistry(PipelineRunConfig{}, t.TempDir())
	if err != nil {
		t.Fatalf("buildStageRegistry: %v", err)
	}
	for name, stage := range registry {
		profile, ok := schemas.BuiltinCapabilities(name)
		if !ok {
			continue // a custom node has no builtin profile
		}
		runtime := stage.Capabilities()
		if profile.ModelFree != runtime.ModelFree {
			t.Errorf("%s: profile model_free = %v, stage declares %v", name, profile.ModelFree, runtime.ModelFree)
		}
		if got := profile.PullContext != nil && *profile.PullContext; got != runtime.PullContext {
			t.Errorf("%s: profile pull_context = %v, stage declares %v", name, got, runtime.PullContext)
		}
		if got := profile.PullMemory != nil && *profile.PullMemory; got != runtime.ConsumesMemory {
			t.Errorf("%s: profile pull_memory = %v, stage declares consumes_memory = %v", name, got, runtime.ConsumesMemory)
		}
	}
}
