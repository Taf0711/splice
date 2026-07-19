package stages

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

//go:embed prompts/step_back.md
var stepBackSystemPrompt string

const stepBackToolName = "submit_step_back"

// StepBackReport is the compressed input to the step-back analysis.
// It is built by the orchestrator from current state and trajectory history.
type StepBackReport struct {
	Intent       string    `json:"intent"`
	RecentScores []float64 `json:"recent_scores"`
	FailingTests []string  `json:"failing_tests,omitempty"`
	ChangedFiles []string  `json:"changed_files,omitempty"`
	Reason       string    `json:"reason"`
}

// StepBack runs a fresh-context step-back analysis when the trajectory
// plateaus. It is orchestrator-level, not a pipeline stage, and does not
// appear in StageRecords.
func StepBack(ctx context.Context, provider zeroruntime.Provider, opts StageOptions, report StepBackReport) (schemas.StepBackAnalysis, error) {
	if provider == nil {
		return schemas.StepBackAnalysis{}, fmt.Errorf("step back requires a provider")
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return schemas.StepBackAnalysis{}, fmt.Errorf("marshal report: %w", err)
	}
	collected, err := callValidatedToolUse(ctx, provider, opts.model("claude-sonnet-4"), opts.ReasoningEffort, composeSystemPrompt(stepBackSystemPrompt), string(payload), opts.Images, stepBackToolDefinition(), &opts.Stream, parseStepBackAnalysis)
	if err != nil {
		return schemas.StepBackAnalysis{}, err
	}
	return decodeStepBackAnalysis(collected)
}

func parseStepBackAnalysis(collected *zeroruntime.CollectedStream) error {
	_, err := decodeStepBackAnalysis(collected)
	return err
}

func decodeStepBackAnalysis(collected *zeroruntime.CollectedStream) (schemas.StepBackAnalysis, error) {
	tc := findToolCall(collected, stepBackToolName)
	if tc == nil {
		return schemas.StepBackAnalysis{}, fmt.Errorf("model did not call %s", stepBackToolName)
	}
	var analysis schemas.StepBackAnalysis
	if err := json.Unmarshal([]byte(tc.Arguments), &analysis); err != nil {
		return schemas.StepBackAnalysis{}, fmt.Errorf("parse %s: %w", stepBackToolName, err)
	}
	if err := analysis.Validate(); err != nil {
		return schemas.StepBackAnalysis{}, err
	}
	return analysis, nil
}

func stepBackToolDefinition() zeroruntime.ToolDefinition {
	return zeroruntime.ToolDefinition{
		Name:        stepBackToolName,
		Description: "Submit the step-back analysis for a coding plateau.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"hypothesized_root_cause": map[string]any{"type": "string"},
				"evidence": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"recommended_approach": map[string]any{"type": "string"},
				"confidence":           map[string]any{"type": "number"},
			},
			"required": []string{"hypothesized_root_cause", "recommended_approach", "confidence"},
		},
	}
}
