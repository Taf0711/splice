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

// stagesForTier builds the ordered execution-stage list and token budget for a
// tier. Shared by BuildExecutionPlan and BuildExecutionPlanForTask so the
// tier-to-stages shape lives in one place.
func stagesForTier(tier schemas.PipelineTier) ([]schemas.ExecutionStage, schemas.TokenBudget, error) {
	budget, err := BudgetForTier(tier)
	if err != nil {
		return nil, schemas.TokenBudget{}, err
	}
	names, err := StageNamesForTier(tier)
	if err != nil {
		return nil, schemas.TokenBudget{}, err
	}
	stages := make([]schemas.ExecutionStage, 0, len(budget.PerStage))
	for _, name := range names {
		stages = append(stages, schemas.ExecutionStage{
			Name:      name,
			Budget:    budget.PerStage[name],
			DependsOn: nil,
		})
	}
	return stages, budget, nil
}

// BuildExecutionPlan builds a minimal execution plan for the current request.
func BuildExecutionPlan(request string) (schemas.ExecutionPlan, error) {
	tier := ClassifyRequest(request)
	stages, budget, err := stagesForTier(tier)
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
		Stages:        stages,
		TokenBudget:   budget,
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
	tier := task.EstimatedTier
	if tier == nil || *tier == "" {
		computed := ClassifyRequest(task.Intent)
		tier = &computed
	}
	stages, budget, err := stagesForTier(*tier)
	if err != nil {
		return schemas.ExecutionPlan{}, nil, err
	}
	acceptanceFacts := make([]string, 0, len(task.AcceptanceFacts))
	for _, fact := range task.AcceptanceFacts {
		acceptanceFacts = append(acceptanceFacts, fact.Statement)
	}
	return schemas.ExecutionPlan{
		Tier:          *tier,
		RequestIntent: task.Intent,
		Stages:        stages,
		TokenBudget:   budget,
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
