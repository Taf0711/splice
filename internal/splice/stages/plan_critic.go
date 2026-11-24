package stages

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

//go:embed prompts/plan_critic.md
var planCriticSystemPrompt string

const planCriticToolName = "submit_critique"

// PlanCritic is the adversarial plan review stage.
type PlanCritic struct{}

var _ Stage = PlanCritic{}

func (PlanCritic) Capabilities() Capabilities {
	return Capabilities{Description: "reviewing the plan"}
}

func (PlanCritic) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options StageOptions) (schemas.HarnessStageOutput, error) {
	if provider == nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("plan critic requires a provider")
	}
	if options.Plan == nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("plan critic requires options.plan")
	}
	critiqueInput := schemas.PlanCriticInput{
		Plan:             *options.Plan,
		PreviousPlan:     options.PreviousPlan,
		PreviousCritique: options.PreviousCritique,
		RelevantContext:  options.RelevantContext,
		PipelineStages:   input.PipelineStages,
		NextStage:        input.NextStage,
	}
	if err := critiqueInput.Validate(); err != nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("plan critic input: %w", err)
	}
	payload, _ := json.MarshalIndent(critiqueInput, "", "  ")
	collected, err := callValidatedToolUse(ctx, provider, options.model("reasoning"), options.ReasoningEffort, composeSystemPrompt(planCriticSystemPrompt, options.ProjectInstructions), string(payload), options.Images, []zeroruntime.ToolDefinition{planCriticToolDefinition()}, options.MaxOutputTokens, &options.Stream, parsePlanCritique, options.PromptCacheKey, options.OnFormatRetry)
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
		excerpt := tc.Arguments
		if len(excerpt) > 160 {
			excerpt = excerpt[:160]
		}
		excerpt = strings.ReplaceAll(excerpt, "\n", " ")
		return schemas.PlanCritique{}, fmt.Errorf("parse %s: %w (arguments began: %q)%s", planCriticToolName, err, excerpt, jsonErrorContext(tc.Arguments, err))
	}
	// The flag is fully determined by the severities, so derive it rather
	// than reject a model that sent it inconsistently.
	critique.MustFixBeforeExecution = critique.BlocksExecution()
	if err := critique.Validate(); err != nil {
		return schemas.PlanCritique{}, err
	}
	return critique, nil
}

// jsonErrorContext returns the text around the byte where a JSON decode
// failed. It returns "" when err has no offset. The text is 60 bytes on each
// side of the failing byte, cut at rune boundaries, with newlines replaced by
// spaces. The result starts with a space so that the caller can append it.
func jsonErrorContext(args string, err error) string {
	var (
		offset int64
		field  string
		syn    *json.SyntaxError
		typ    *json.UnmarshalTypeError
	)
	switch {
	case errors.As(err, &syn):
		offset = syn.Offset
	case errors.As(err, &typ):
		offset = typ.Offset
		field = typ.Field
	default:
		return ""
	}
	pos := min(max(int(offset), 0), len(args))
	idx := max(pos-1, 0)
	start := max(idx-60, 0)
	end := min(idx+60, len(args))
	for start < end && !utf8.RuneStart(args[start]) {
		start++
	}
	for end < len(args) && !utf8.RuneStart(args[end]) {
		end++
	}
	snippet := strings.ReplaceAll(args[start:end], "\n", " ")
	if field != "" {
		return fmt.Sprintf(" (field %q, near byte %d: %q)", field, pos, snippet)
	}
	return fmt.Sprintf(" (near byte %d: %q)", pos, snippet)
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
							"category":             map[string]any{"type": "string", "enum": schemas.CritiqueCategories()},
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
