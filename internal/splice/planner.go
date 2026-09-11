package splice

import (
	"strings"
	"unicode"

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

// stagesForTier builds the ordered execution-stage list and token budget for a
// tier by compiling the embedded default topology. Shared by
// BuildExecutionPlan and BuildExecutionPlanForTask so the tier-to-stages shape
// lives in one place.
func stagesForTier(tier schemas.PipelineTier) ([]schemas.ExecutionStage, schemas.TokenBudget, error) {
	compiled, err := compileForTier(tier)
	if err != nil {
		return nil, schemas.TokenBudget{}, err
	}
	return compiled.Stages, compiled.Budget, nil
}

// BuildExecutionPlan builds a minimal execution plan for the current request
// from the embedded default topology.
func BuildExecutionPlan(request string) (schemas.ExecutionPlan, error) {
	return BuildExecutionPlanWithTopology(defaultTopology(), request)
}

// BuildExecutionPlanWithTopology builds the plan for one request from the given
// topology. A nil topology compiles the embedded default, so a legacy caller
// keeps today's behavior.
func BuildExecutionPlanWithTopology(topology *schemas.PipelineTopology, request string) (schemas.ExecutionPlan, error) {
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
	return schemas.ExecutionPlan{
		Tier:          tier,
		RequestIntent: intent,
		Stages:        compiled.Stages,
		TokenBudget:   compiled.Budget,
		Warnings:      append([]string(nil), compiled.Warnings...),
		TopologyName:  topology.Name,
	}, nil
}

// BuildExecutionPlanForTask builds a plan for a design task.
func BuildExecutionPlanForTask(task schemas.Task) (schemas.ExecutionPlan, error) {
	plan, _, err := BuildExecutionPlanForTaskWithFacts(task)
	return plan, err
}

// BuildExecutionPlanForTaskWithFacts builds a plan for a design task and
// extracts acceptance fact statements for context injection.
func BuildExecutionPlanForTaskWithFacts(task schemas.Task) (schemas.ExecutionPlan, []string, error) {
	tier := ClassifyRequest(task.Intent)
	for _, fact := range task.AcceptanceFacts {
		if fact.AutomatedVerification && tier == schemas.TierTrivial {
			tier = schemas.TierLight
			break
		}
	}
	compiled, err := compileForTier(tier)
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
		Stages:          compiled.Stages,
		TokenBudget:     compiled.Budget,
		AcceptanceFacts: append([]schemas.AcceptanceFact(nil), task.AcceptanceFacts...),
		Warnings:        append([]string(nil), compiled.Warnings...),
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
