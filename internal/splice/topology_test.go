package splice

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

var allTiers = []schemas.PipelineTier{
	schemas.TierTrivial,
	schemas.TierLight,
	schemas.TierStandard,
	schemas.TierSubstantial,
	schemas.TierArchitectural,
}

// TestDefaultTopologyCompilesEqualToBudgetTable is the dogfood equivalence
// contract for the whole track (plan section 4.2). For every tier the compiled
// default must reproduce the ordered stage names, each stage budget, and the
// token budget totals, reserve, and overflow policy.
func TestDefaultTopologyCompilesEqualToBudgetTable(t *testing.T) {
	for _, tier := range allTiers {
		t.Run(string(tier), func(t *testing.T) {
			compiled, err := CompileTopology(defaultTopology(), tier)
			if err != nil {
				t.Fatalf("CompileTopology(default, %s) = %v", tier, err)
			}
			wantNames, err := StageNamesForTier(tier)
			if err != nil {
				t.Fatalf("StageNamesForTier(%s) = %v", tier, err)
			}
			gotNames := make([]string, 0, len(compiled.Stages))
			for _, stage := range compiled.Stages {
				gotNames = append(gotNames, stage.Name)
			}
			if !reflect.DeepEqual(gotNames, wantNames) {
				t.Fatalf("compiled stage order = %v, want %v", gotNames, wantNames)
			}
			wantBudget, err := BudgetForTier(tier)
			if err != nil {
				t.Fatalf("BudgetForTier(%s) = %v", tier, err)
			}
			for _, stage := range compiled.Stages {
				if got, want := stage.Budget, wantBudget.PerStage[stage.Name]; got != want {
					t.Fatalf("%s stage %s budget = %+v, want %+v", tier, stage.Name, got, want)
				}
			}
			if !reflect.DeepEqual(compiled.Budget, wantBudget) {
				t.Fatalf("compiled budget = %+v, want %+v", compiled.Budget, wantBudget)
			}
		})
	}
}

// TestDefaultTopologyDependsOn pins the compiled dependency shape. Every edge
// connects a node that is active everywhere its dependent is active, so tier
// filtering never leaves a dangling dependency.
func TestDefaultTopologyDependsOn(t *testing.T) {
	compiled, err := CompileTopology(defaultTopology(), schemas.TierSubstantial)
	if err != nil {
		t.Fatalf("CompileTopology = %v", err)
	}
	want := map[string][]string{
		"code_writer":         nil,
		"test_generator":      {"code_writer"},
		"static_analyzer":     {"code_writer"},
		"security_auditor":    {"test_generator"},
		"test_runner":         {"static_analyzer"},
		"acceptance_verifier": {"test_runner"},
	}
	for _, stage := range compiled.Stages {
		if !reflect.DeepEqual(stage.DependsOn, want[stage.Name]) {
			t.Fatalf("stage %s depends_on = %v, want %v", stage.Name, stage.DependsOn, want[stage.Name])
		}
	}
	if got := len(compiled.Warnings); got != 0 {
		t.Fatalf("default topology warnings = %v, want none", compiled.Warnings)
	}
}

func TestCompileTopologyCustomGraph(t *testing.T) {
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "diamond",
		Nodes: []schemas.PipelineNode{
			{Name: "a", Type: "code_writer"},
			{Name: "b", Type: "static_analyzer"},
			{Name: "c", Type: "test_runner"},
			{Name: "d", Type: "acceptance_verifier"},
			{Name: "lonely", Type: schemas.NodeTypeCommand, Command: []string{"true"}},
		},
		Edges: []schemas.PipelineEdge{
			{From: "a", To: "b"},
			{From: "a", To: "c"},
			{From: "b", To: "d"},
			{From: "c", To: "d"},
		},
	}
	compiled, err := CompileTopology(topology, schemas.TierStandard)
	if err != nil {
		t.Fatalf("CompileTopology = %v", err)
	}
	if len(compiled.Stages) != 5 {
		t.Fatalf("stages = %d, want 5", len(compiled.Stages))
	}
	order := make(map[string]int, len(compiled.Stages))
	for i, stage := range compiled.Stages {
		order[stage.Name] = i
	}
	if order["a"] > order["b"] || order["b"] > order["d"] || order["c"] > order["d"] {
		t.Fatalf("topological order violated: %v", order)
	}
	if got := compiled.Stages[order["lonely"]].Budget; got != (schemas.StageBudget{}) {
		t.Fatalf("command node budget = %+v, want zero", got)
	}
}

func TestCompileTopologyRejects(t *testing.T) {
	cases := []struct {
		name     string
		topology *schemas.PipelineTopology
		tier     schemas.PipelineTier
		wantSub  string
	}{
		{
			name: "no node active at tier",
			topology: &schemas.PipelineTopology{
				Version: 1, Name: "empty",
				Nodes: []schemas.PipelineNode{{Name: "sec", Type: "security_auditor", Tiers: []schemas.PipelineTier{schemas.TierSubstantial}}},
			},
			tier:    schemas.TierTrivial,
			wantSub: "no nodes are active at tier",
		},
		{
			name: "dangling dependency",
			topology: &schemas.PipelineTopology{
				Version: 1, Name: "dangling",
				Nodes: []schemas.PipelineNode{
					{Name: "sec", Type: "security_auditor", Tiers: []schemas.PipelineTier{schemas.TierSubstantial}},
					{Name: "tr", Type: "test_runner", Tiers: []schemas.PipelineTier{schemas.TierLight}},
				},
				Edges: []schemas.PipelineEdge{{From: "sec", To: "tr"}},
			},
			tier:    schemas.TierLight,
			wantSub: "depends on sec, which is filtered out",
		},
		{
			name: "over envelope",
			topology: &schemas.PipelineTopology{
				Version: 1, Name: "fat",
				Nodes: []schemas.PipelineNode{{Name: "code_writer", Type: "code_writer", Budget: &schemas.StageBudget{InputMax: 999999, OutputMax: 1}}},
			},
			tier:    schemas.TierLight,
			wantSub: "over the",
		},
		{
			name:     "nil topology",
			topology: nil,
			tier:     schemas.TierLight,
			wantSub:  "topology is nil",
		},
		{
			name: "invalid tier",
			topology: &schemas.PipelineTopology{
				Version: 1, Name: "x",
				Nodes: []schemas.PipelineNode{{Name: "code_writer", Type: "code_writer"}},
			},
			tier:    schemas.PipelineTier("nope"),
			wantSub: "unknown pipeline tier",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CompileTopology(tc.topology, tc.tier)
			if err == nil {
				t.Fatalf("CompileTopology() = nil, want error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("CompileTopology() = %q, want error containing %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestCompileTopologyWarnings(t *testing.T) {
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "warn",
		Nodes: []schemas.PipelineNode{
			{Name: "gen", Type: "test_generator"},
			{Name: "lint", Type: schemas.NodeTypeCommand, Command: []string{"true"}},
			{Name: "writer", Type: "code_writer"},
		},
		Edges: []schemas.PipelineEdge{{From: "lint", To: "writer"}},
	}
	compiled, err := CompileTopology(topology, schemas.TierStandard)
	if err != nil {
		t.Fatalf("CompileTopology = %v", err)
	}
	joined := strings.Join(compiled.Warnings, "\n")
	if !strings.Contains(joined, "no code_writer upstream edge") {
		t.Fatalf("warnings = %v, want the test_generator coupling warning", compiled.Warnings)
	}
	if !strings.Contains(joined, "untrusted data") {
		t.Fatalf("warnings = %v, want the command-to-model edge warning", compiled.Warnings)
	}
}

func TestStagesForTierMatchesCompiledDefault(t *testing.T) {
	for _, tier := range allTiers {
		stages, budget, err := stagesForTier(tier)
		if err != nil {
			t.Fatalf("stagesForTier(%s) = %v", tier, err)
		}
		compiled, err := CompileTopology(defaultTopology(), tier)
		if err != nil {
			t.Fatalf("CompileTopology(%s) = %v", tier, err)
		}
		if !reflect.DeepEqual(stages, compiled.Stages) {
			t.Fatalf("%s stagesForTier != compiled stages", tier)
		}
		if !reflect.DeepEqual(budget, compiled.Budget) {
			t.Fatalf("%s stagesForTier budget != compiled budget", tier)
		}
	}
}
