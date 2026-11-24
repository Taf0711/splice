package stages

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
	critique, repairs, err := decodePlanCritique(collected)
	if err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	summary := fmt.Sprintf("Plan critique: %d issue(s)", len(critique.Critiques))
	data := map[string]any{
		"plan_critic_input":  critiqueInput,
		"plan_critic_output": critique,
	}
	if len(repairs) > 0 {
		summary += " Repaired model output: " + strings.Join(repairs, "; ") + "."
		data["model_output_repairs"] = repairs
	}
	return schemas.HarnessStageOutput{
		Summary:    summary,
		Detail:     critique.OverallAssessment,
		Confidence: 1.0,
		Data:       data,
		Usage:      usageFromCollected(collected),
	}, nil
}

func parsePlanCritique(collected *zeroruntime.CollectedStream) error {
	_, _, err := decodePlanCritique(collected)
	return err
}

// decodePlanCritique decodes the submit_critique arguments. It returns the
// critique and a short phrase for each repair it applied to the model output.
// The phrases are empty when the model output needed no repair.
func decodePlanCritique(collected *zeroruntime.CollectedStream) (schemas.PlanCritique, []string, error) {
	tc := findToolCall(collected, planCriticToolName)
	if tc == nil {
		return schemas.PlanCritique{}, nil, fmt.Errorf("model did not call %s", planCriticToolName)
	}
	text := tc.Arguments
	var repairs []string
	var critique schemas.PlanCritique
	if err := json.Unmarshal([]byte(text), &critique); err != nil {
		fixed, repaired, ok := repairPlanCritique(text, err)
		if !ok {
			excerpt := tc.Arguments
			if len(excerpt) > 160 {
				excerpt = excerpt[:160]
			}
			excerpt = strings.ReplaceAll(excerpt, "\n", " ")
			return schemas.PlanCritique{}, nil, fmt.Errorf("parse %s: %w (arguments began: %q)%s", planCriticToolName, err, excerpt, jsonErrorContext(tc.Arguments, err))
		}
		text, critique = fixed, repaired
		repairs = append(repairs, "last value had no quotes")
	}
	shapeRepairs, err := planCriticShapeRepairs(text)
	if err != nil {
		return schemas.PlanCritique{}, nil, err
	}
	repairs = append(repairs, shapeRepairs...)
	critique, flagCorrected, err := finishPlanCritique(critique)
	if err != nil {
		return schemas.PlanCritique{}, nil, err
	}
	if flagCorrected {
		repairs = append(repairs, "must_fix_before_execution corrected to match severities")
	}
	return critique, repairs, nil
}

// planCriticShapeRepairs names each list field that the model sent in a shape
// that the schema decodes by repair. It reads the raw JSON, so it runs on the
// same text that the schema decoded.
func planCriticShapeRepairs(text string) ([]string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &fields); err != nil {
		return nil, fmt.Errorf("read %s fields: %w", planCriticToolName, err)
	}
	var repairs []string
	if raw := bytes.TrimSpace(fields["critiques"]); len(raw) > 0 && raw[0] == '"' {
		repairs = append(repairs, "critiques sent as a string")
	}
	if raw := bytes.TrimSpace(fields["cross_cutting_concerns"]); len(raw) > 0 && raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("read cross_cutting_concerns: %w", err)
		}
		// A blank string is an empty list sent as a string.
		content := strings.TrimSpace(s)
		if content == "" || (content[0] == '[' && json.Valid([]byte(content))) {
			repairs = append(repairs, "cross_cutting_concerns sent as a string")
		} else {
			repairs = append(repairs, "cross_cutting_concerns sent as one sentence")
		}
	}
	return repairs, nil
}

// finishPlanCritique runs the post-decode steps shared by every decode path.
// It reports whether the model flag differed from the derived flag.
func finishPlanCritique(critique schemas.PlanCritique) (schemas.PlanCritique, bool, error) {
	// The flag is fully determined by the severities, so derive it rather
	// than reject a model that sent it inconsistently.
	derived := critique.BlocksExecution()
	corrected := critique.MustFixBeforeExecution != derived
	critique.MustFixBeforeExecution = derived
	if err := critique.Validate(); err != nil {
		return schemas.PlanCritique{}, false, err
	}
	return critique, corrected, nil
}

// repairPlanCritique tries one repair of arguments that failed with a syntax
// error. It returns the repaired text and its decoded critique. It returns false
// when the first error is not a syntax error, or when the repair does not apply
// or does not decode.
func repairPlanCritique(args string, first error) (string, schemas.PlanCritique, bool) {
	var syn *json.SyntaxError
	if !errors.As(first, &syn) {
		return "", schemas.PlanCritique{}, false
	}
	fixed, ok := repairUnquotedTrailingValue(args)
	if !ok {
		return "", schemas.PlanCritique{}, false
	}
	var critique schemas.PlanCritique
	if err := json.Unmarshal([]byte(fixed), &critique); err != nil {
		return "", schemas.PlanCritique{}, false
	}
	return fixed, critique, true
}

// planCriticKeyPattern matches a JSON key string, its colon, and the spaces
// around the colon.
var planCriticKeyPattern = regexp.MustCompile(`"([^"\\]|\\.)*"\s*:\s*`)

// repairUnquotedTrailingValue rebuilds arguments whose last string value has
// no quotes, such as `"overall_assessment": The plan is fine.}`. It quotes the
// bare text that follows the last key whose value is not a JSON value. It
// returns false when the text does not fit that shape.
func repairUnquotedTrailingValue(args string) (string, bool) {
	args = strings.TrimSpace(args)
	if !strings.HasSuffix(args, "}") {
		return "", false
	}
	body := args[:len(args)-1]
	valueStart := -1
	for _, m := range planCriticKeyPattern.FindAllStringIndex(body, -1) {
		if !startsJSONValue(body[m[1]:]) {
			valueStart = m[1]
		}
	}
	if valueStart < 0 {
		return "", false
	}
	bare := strings.TrimRight(body[valueStart:], " \t\r\n")
	if bare == "" {
		return "", false
	}
	// A quote followed later by a colon means the bare text swallowed another
	// key. Repairing that would hide the other fields.
	if q := strings.IndexByte(bare, '"'); q >= 0 && strings.Contains(bare[q+1:], ":") {
		return "", false
	}
	quoted, err := json.Marshal(bare)
	if err != nil {
		return "", false
	}
	return body[:valueStart] + string(quoted) + "}", true
}

// startsJSONValue reports whether s begins with a JSON value, or with text
// that the repair must leave alone.
func startsJSONValue(s string) bool {
	if s == "" {
		return true
	}
	if strings.IndexByte(`"{[-`, s[0]) >= 0 || (s[0] >= '0' && s[0] <= '9') {
		return true
	}
	for _, lit := range []string{"true", "false", "null"} {
		if strings.HasPrefix(s, lit) {
			return true
		}
	}
	return false
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
