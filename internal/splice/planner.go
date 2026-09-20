package splice

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

const maxIntentChars = 320

// ClassifyRequest returns the pipeline tier for a raw user request.
func ClassifyRequest(request string) schemas.PipelineTier {
	return ClassifyRequestTyped(request).Tier
}

// compileForTier compiles the embedded default topology for a tier. It is the
// single source for the tier's stages, budget, and compile warnings, so the
// warnings cannot be dropped between the compiler and the plan.
func compileForTier(tier schemas.PipelineTier) (CompiledTopology, error) {
	return CompileTopology(defaultTopology(), tier)
}

// filterStagesForFlags removes stages disabled by the resolved flag set. It
// filters compiled stages rather than rebuilding them from stage names, so
// topology-compiled per-stage data (including DependsOn edges) is preserved.// This is the flags spec's DD8 rule: with topology-core live, filtering
// applies to compiled.Stages, not to StageNamesForTier output.
//
// A stage that depends on a removed stage cannot run: its input edge can
// never be satisfied, and edge scoping would treat the dangling dependency as
// scope-to-nothing, which looks like success. Such dependents are removed
// too, transitively, and each cascade removal returns a named warning for
// the plan. Nothing dangles silently.
func filterStagesForFlags(compiled CompiledTopology, set flags.Set) ([]schemas.ExecutionStage, []string) {
	disabled := make(map[string]bool)
	for _, stage := range compiled.Stages {
		if flag, ok := stageFlag[stage.Name]; ok && !set.Enabled(flag) {
			disabled[stage.Name] = true
		}
	}
	if len(disabled) == 0 {
		return append([]schemas.ExecutionStage(nil), compiled.Stages...), nil
	}
	warnings := make([]string, 0, len(disabled))
	removed := make(map[string]bool, len(disabled))
	for name := range disabled {
		removed[name] = true
	}
	for changed := true; changed; {
		changed = false
		for _, stage := range compiled.Stages {
			if removed[stage.Name] {
				continue
			}
			for _, dependency := range stage.DependsOn {
				if removed[dependency] {
					removed[stage.Name] = true
					warnings = append(warnings, fmt.Sprintf("stage %q removed: dependency %q is disabled by flag", stage.Name, dependency))
					changed = true
					break
				}
			}
		}
	}
	filtered := make([]schemas.ExecutionStage, 0, len(compiled.Stages))
	for _, stage := range compiled.Stages {
		if !removed[stage.Name] {
			filtered = append(filtered, stage)
		}
	}
	return filtered, warnings
}

// stagesForTier builds the ordered execution-stage list and token budget for a
// tier by compiling the embedded default topology, then removes stages
// disabled by the resolved flag set. Shared by BuildExecutionPlan and
// BuildExecutionPlanForTask so the tier-to-stages shape lives in one place.
func stagesForTier(tier schemas.PipelineTier, set flags.Set) ([]schemas.ExecutionStage, schemas.TokenBudget, []string, error) {
	compiled, err := compileForTier(tier)
	if err != nil {
		return nil, schemas.TokenBudget{}, nil, err
	}
	stages, cascade := filterStagesForFlags(compiled, set)
	return stages, compiled.Budget, append(append([]string(nil), compiled.Warnings...), cascade...), nil
}

// BuildExecutionPlan builds a minimal execution plan for the current request
// from the embedded default topology.
func BuildExecutionPlan(request string, set flags.Set) (schemas.ExecutionPlan, error) {
	return BuildExecutionPlanWithTopology(nil, request, set)
}

// BuildExecutionPlanWithTopology builds the plan for one request from the given
// topology. A nil topology compiles the embedded default, so a legacy caller
// keeps today's behavior. Stages disabled by the resolved flag set are removed
// from the plan, and the enabled names are recorded as plan provenance.
func BuildExecutionPlanWithTopology(topology *schemas.PipelineTopology, request string, set flags.Set) (schemas.ExecutionPlan, error) {
	if topology == nil {
		topology = defaultTopology()
	}
	tier := ClassifyRequest(request)
	compiled, err := CompileTopology(topology, tier)
	if err != nil {
		return schemas.ExecutionPlan{}, err
	}
	intent := DistillRequestIntent(request)
	if intent == "" {
		intent = "image-only request"
	}
	stages, cascade := filterStagesForFlags(compiled, set)
	return schemas.ExecutionPlan{
		Tier:          tier,
		RequestIntent: intent,
		Stages:        stages,
		TokenBudget:   compiled.Budget,
		Flags:         set.EnabledNames(),
		Warnings:      append(append([]string(nil), compiled.Warnings...), cascade...),
		TopologyName:  topology.Name,
	}, nil
}

// BuildExecutionPlanForTask builds a plan for a design task.
func BuildExecutionPlanForTask(task schemas.Task, set flags.Set) (schemas.ExecutionPlan, error) {
	plan, _, err := BuildExecutionPlanForTaskWithFacts(task, set)
	return plan, err
}

// BuildExecutionPlanForTaskWithFacts builds a plan for a design task and
// extracts acceptance fact statements for context injection.
func BuildExecutionPlanForTaskWithFacts(task schemas.Task, set flags.Set) (schemas.ExecutionPlan, []string, error) {
	tier := ClassifyRequest(task.Intent)
	for _, fact := range task.AcceptanceFacts {
		if fact.AutomatedVerification && tier == schemas.TierTrivial {
			tier = schemas.TierLight
			break
		}
	}
	stages, budget, warnings, err := stagesForTier(tier, set)
	if err != nil {
		return schemas.ExecutionPlan{}, nil, err
	}
	acceptanceFacts := make([]string, 0, len(task.AcceptanceFacts))
	for _, fact := range task.AcceptanceFacts {
		acceptanceFacts = append(acceptanceFacts, fact.Statement)
	}
	return schemas.ExecutionPlan{
		Tier:            tier,
		RequestIntent:   task.Intent,
		Stages:          stages,
		TokenBudget:     budget,
		AcceptanceFacts: append([]schemas.AcceptanceFact(nil), task.AcceptanceFacts...),
		Flags:           set.EnabledNames(),
		Warnings:        warnings,
	}, acceptanceFacts, nil
}

// DistillRequestIntent returns a bounded deterministic intent summary for downstream agents.
func DistillRequestIntent(request string) string {
	normalized := strings.Join(strings.Fields(request), " ")
	runes := []rune(normalized)
	if len(runes) <= maxIntentChars {
		return normalized
	}
	truncated := string(runes[:maxIntentChars-3])
	truncated = strings.TrimRightFunc(truncated, unicode.IsSpace)
	return truncated + "..."
}
