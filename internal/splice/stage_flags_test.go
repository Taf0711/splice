package splice

import (
	"reflect"
	"testing"

	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// TestFilterStageNamesKeepsDefaults is the safety property: the zero flag set
// must not change the stage shape. Every tier keeps its full stage list.
func TestFilterStageNamesKeepsDefaults(t *testing.T) {
	var zero flags.Set
	for _, tier := range []schemas.PipelineTier{
		schemas.TierTrivial, schemas.TierLight, schemas.TierStandard,
		schemas.TierSubstantial, schemas.TierArchitectural,
	} {
		names, err := StageNamesForTier(tier)
		if err != nil {
			t.Fatalf("tier %q: %v", tier, err)
		}
		if got := FilterStageNames(names, zero); !reflect.DeepEqual(got, names) {
			t.Fatalf("tier %q: zero flag set changed stages: got %v want %v", tier, got, names)
		}
	}
}

func TestFilterStageNamesRemovesDisabledStages(t *testing.T) {
	set, err := flags.Resolve(flags.Sources{User: map[string]bool{
		string(flags.StageSecurityAuditor): false,
		string(flags.StageTestGenerator):   false,
	}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	names, err := StageNamesForTier(schemas.TierSubstantial)
	if err != nil {
		t.Fatalf("StageNamesForTier: %v", err)
	}
	got := FilterStageNames(names, set)
	for _, removed := range []string{"security_auditor", "test_generator"} {
		for _, name := range got {
			if name == removed {
				t.Fatalf("disabled stage %q survived filtering: %v", removed, got)
			}
		}
	}
	for _, kept := range []string{"code_writer", "test_runner", "acceptance_verifier"} {
		found := false
		for _, name := range got {
			if name == kept {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("stage %q must never be filtered: %v", kept, got)
		}
	}
}

// TestStageFlagMappingIsValid is the pairing guard for the stage-to-flag map.
// code_writer must never be filterable, every mapped stage must be a real
// stage, and every mapped flag must be registered.
func TestStageFlagMappingIsValid(t *testing.T) {
	allStages := make(map[string]bool)
	for _, tier := range []schemas.PipelineTier{
		schemas.TierTrivial, schemas.TierLight, schemas.TierStandard,
		schemas.TierSubstantial, schemas.TierArchitectural,
	} {
		names, err := StageNamesForTier(tier)
		if err != nil {
			t.Fatalf("tier %q: %v", tier, err)
		}
		for _, name := range names {
			allStages[name] = true
		}
	}
	if _, ok := stageFlag["code_writer"]; ok {
		t.Fatal("code_writer must not be filterable: it produces the change")
	}
	for stage, flag := range stageFlag {
		if !allStages[stage] {
			t.Errorf("stageFlag maps %q, which is not a stage in any tier", stage)
		}
		def, ok := flags.Lookup(flag)
		if !ok {
			t.Errorf("stageFlag maps %q to unregistered flag %q", stage, flag)
			continue
		}
		if def.ReadBy != "splice.FilterStageNames" {
			t.Errorf("flag %q declares ReadBy %q, want splice.FilterStageNames", flag, def.ReadBy)
		}
	}
}

// TestBuildExecutionPlanRecordsFlags pins plan provenance and the wiring from
// the plan builder to the filter.
func TestBuildExecutionPlanRecordsFlags(t *testing.T) {
	prompt := "Add a new database table for user profiles and wire the migration"

	// Defaults: the plan records every enabled flag and keeps the full shape.
	base, err := BuildExecutionPlan(prompt, nil)
	if err != nil {
		t.Fatalf("BuildExecutionPlan: %v", err)
	}
	if base.Flags == nil {
		t.Fatal("plan must record the enabled flag names")
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("default plan must validate: %v", err)
	}

	// Disable a stage: it leaves the plan, and the flag list still validates.
	set, err := flags.Resolve(flags.Sources{User: map[string]bool{
		string(flags.StageTestGenerator): false,
	}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	filtered, err := BuildExecutionPlan(prompt, set)
	if err != nil {
		t.Fatalf("BuildExecutionPlan filtered: %v", err)
	}
	if err := filtered.Validate(); err != nil {
		t.Fatalf("filtered plan must validate: %v", err)
	}
	if len(filtered.Stages) >= len(base.Stages) {
		t.Fatalf("disabling a stage must shrink the plan: base %d filtered %d", len(base.Stages), len(filtered.Stages))
	}
	for _, stage := range filtered.Stages {
		if stage.Name == "test_generator" {
			t.Fatal("disabled stage test_generator is still in the plan")
		}
	}
}
