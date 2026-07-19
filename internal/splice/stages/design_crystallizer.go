package stages

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

//go:embed prompts/design_crystallizer.md
var designCrystallizeSystemPrompt string

// DesignCrystallizer turns a finished design conversation into a typed
// DesignPlan. It is a standalone typed agent (own struct, prompt, tool
// definition, and stage name "design_crystallize") so it can be
// independently model-routed and, later, represented as its own node in a
// user-configurable pipeline topology.
type DesignCrystallizer struct{}

// Crystallize turns the conversation into a DesignPlan.
func (DesignCrystallizer) Crystallize(ctx context.Context, provider zeroruntime.Provider, opts StageOptions, input schemas.DesignConversationInput) (schemas.DesignPlan, error) {
	if provider == nil {
		return schemas.DesignPlan{}, fmt.Errorf("crystallize requires a provider")
	}
	if err := input.Validate(); err != nil {
		return schemas.DesignPlan{}, fmt.Errorf("crystallize input: %w", err)
	}
	payload, _ := json.MarshalIndent(input, "", "  ")
	collected, err := callValidatedToolUse(ctx, provider, opts.model("medium"), opts.ReasoningEffort, composeSystemPrompt(designCrystallizeSystemPrompt), string(payload), opts.Images, designPlanToolDefinition(), &opts.Stream, parseDesignPlan)
	if err != nil {
		return schemas.DesignPlan{}, err
	}
	return decodeDesignPlan(collected)
}

func parseDesignPlan(collected *zeroruntime.CollectedStream) error {
	_, err := decodeDesignPlan(collected)
	return err
}

func decodeDesignPlan(collected *zeroruntime.CollectedStream) (schemas.DesignPlan, error) {
	tc := findToolCall(collected, "submit_design_plan")
	if tc == nil {
		return schemas.DesignPlan{}, fmt.Errorf("model did not call submit_design_plan")
	}
	var plan schemas.DesignPlan
	if err := json.Unmarshal([]byte(tc.Arguments), &plan); err != nil {
		return schemas.DesignPlan{}, fmt.Errorf("parse submit_design_plan: %w", err)
	}
	plan.Source = "conversation"
	if err := plan.Validate(); err != nil {
		return schemas.DesignPlan{}, err
	}
	return plan, nil
}

func designPlanToolDefinition() zeroruntime.ToolDefinition {
	return zeroruntime.ToolDefinition{
		Name:        "submit_design_plan",
		Description: "Submit the complete DesignPlan for the conversation.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"epic":          map[string]any{"type": "string"},
				"requirements":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"in_scope":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"out_of_scope":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"system_design": map[string]any{"type": "string"},
				"tasks": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":         map[string]any{"type": "string"},
							"title":      map[string]any{"type": "string"},
							"intent":     map[string]any{"type": "string"},
							"depends_on": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							"acceptance_facts": map[string]any{
								"type": "array",
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"statement":                          map[string]any{"type": "string"},
										"automated_verification":             map[string]any{"type": "boolean"},
										"verification_command":               map[string]any{"type": "string"},
										"recommended_automated_verification": map[string]any{"type": "boolean"},
									},
									"required": []string{"statement"},
								},
							},
						},
						"required": []string{"id", "title", "intent"},
					},
				},
				"recommended_tier":       map[string]any{"type": "string"},
				"recommended_model_tier": map[string]any{"type": "string"},
			},
			"required": []string{"epic", "requirements", "in_scope", "out_of_scope", "system_design", "tasks"},
		},
	}
}
