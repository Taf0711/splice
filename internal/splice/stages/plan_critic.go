package stages

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

//go:embed prompts/plan_critic.md
var planCriticSystemPrompt string

const planCriticToolName = "submit_critique"

// PlanCritic is the adversarial plan review stage.
type PlanCritic struct{}

var _ Stage = PlanCritic{}

func (PlanCritic) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options StageOptions) (schemas.HarnessStageOutput, error) {
	if provider == nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("plan critic requires a provider")
	}
	if options.Plan == nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("plan critic requires options.plan")
	}
	critiqueInput := schemas.PlanCriticInput{Plan: *options.Plan}
	if err := critiqueInput.Validate(); err != nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("plan critic input: %w", err)
	}
	payload, _ := json.MarshalIndent(critiqueInput, "", "  ")
	collected, err := callValidatedToolUse(ctx, provider, options.model("reasoning"), options.ReasoningEffort, composeSystemPrompt(planCriticSystemPrompt), string(payload), options.Images, planCriticToolDefinition(), &options.Stream, parsePlanCritique)
	if err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	critique, err := decodePlanCritique(collected)
	if err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	return schemas.HarnessStageOutput{
		Summary:    fmt.Sprintf("Plan critique: %d issue(s)", len(critique.Critiques)),
		Detail:     critique.OverallAssessment,
		Confidence: 1.0,
		Data: map[string]any{
			"plan_critic_input":  critiqueInput,
			"plan_critic_output": critique,
		},
		Usage: usageFromCollected(collected),
	}, nil
}

func parsePlanCritique(collected *zeroruntime.CollectedStream) error {
	_, err := decodePlanCritique(collected)
	return err
}

func decodePlanCritique(collected *zeroruntime.CollectedStream) (schemas.PlanCritique, error) {
	tc := findToolCall(collected, planCriticToolName)
	if tc == nil {
		return schemas.PlanCritique{}, fmt.Errorf("model did not call %s", planCriticToolName)
	}
	var critique schemas.PlanCritique
	if err := json.Unmarshal([]byte(tc.Arguments), &critique); err != nil {
		return schemas.PlanCritique{}, fmt.Errorf("parse %s: %w", planCriticToolName, err)
	}
	if err := critique.Validate(); err != nil {
		return schemas.PlanCritique{}, err
	}
	return critique, nil
}

func planCriticToolDefinition() zeroruntime.ToolDefinition {
	return zeroruntime.ToolDefinition{
		Name:        planCriticToolName,
		Description: "Submit the PlanCritique for the reviewed DesignPlan.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"critiques": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"category":             map[string]any{"type": "string"},
							"severity":             map[string]any{"type": "string", "enum": []string{"info", "low", "medium", "high", "critical"}},
							"issue":                map[string]any{"type": "string"},
							"suggested_mitigation": map[string]any{"type": "string"},
						},
						"required": []string{"category", "severity", "issue"},
					},
				},
				"cross_cutting_concerns":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"must_fix_before_execution": map[string]any{"type": "boolean"},
				"overall_assessment":        map[string]any{"type": "string"},
			},
			"required": []string{"critiques", "cross_cutting_concerns", "must_fix_before_execution", "overall_assessment"},
		},
	}
}

// ExtractPlanCritique extracts the typed PlanCritique from a PlanCritic stage
// output. The TUI uses this instead of type-asserting map[string]any.
func ExtractPlanCritique(output schemas.HarnessStageOutput) (schemas.PlanCritique, error) {
	raw, ok := output.Data["plan_critic_output"]
	if !ok || raw == nil {
		return schemas.PlanCritique{}, fmt.Errorf("plan_critic_output not present in stage output")
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return schemas.PlanCritique{}, fmt.Errorf("marshal plan_critic_output: %w", err)
	}
	var critique schemas.PlanCritique
	if err := json.Unmarshal(b, &critique); err != nil {
		return schemas.PlanCritique{}, fmt.Errorf("unmarshal plan_critic_output: %w", err)
	}
	return critique, nil
}
