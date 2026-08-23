package stages

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

//go:embed prompts/test_generator.md
var testGeneratorSystemPrompt string

const testGeneratorToolName = "submit_tests"

const maxWriterChangedPaths = 50

// TestGenerator is the test generation pipeline stage.
type TestGenerator struct{}

var _ Stage = TestGenerator{}

func (TestGenerator) Capabilities() Capabilities {
	return Capabilities{ConsumesMemory: true, PullContext: true, Description: "generating tests"}
}

func (TestGenerator) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options StageOptions) (schemas.HarnessStageOutput, error) {
	if input.Context == nil {
		req := options.contextRequest(input.RequestIntent)
		if req != nil {
			options.report("requesting context: " + req.Reason)
			return schemas.HarnessStageOutput{
				Summary:        "Test Generator requested codebase context.",
				Detail:         req.Reason,
				Confidence:     1.0,
				ContextRequest: req,
			}, nil
		}
	}

	if input.Context != nil {
		options.report(fmt.Sprintf("reviewed %d context item(s)", len(input.Context.Items)))
	}

	relevantContext := append([]string(nil), options.RelevantContext...)
	if prior := input.PriorSummaries["code_writer"]; prior != "" {
		relevantContext = append(relevantContext, "code_writer: "+prior)
	}
	writerChangedPaths := append([]string(nil), input.PriorChangedFiles["code_writer"]...)
	if len(writerChangedPaths) > maxWriterChangedPaths {
		writerChangedPaths = writerChangedPaths[:maxWriterChangedPaths]
	}
	tgInput := schemas.TestGeneratorInput{
		Intent:             input.RequestIntent,
		Language:           options.language("python"),
		TargetPaths:        options.TargetPaths,
		WriterChangedPaths: writerChangedPaths,
		RelevantContext:    relevantContext,
		RevisionContext:    input.RevisionContext,
		Memory:             selectMemory(input.MemoryBundle),
		PipelineStages:     input.PipelineStages,
		NextStage:          input.NextStage,
	}
	if err := tgInput.Validate(); err != nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("test generator input: %w", err)
	}

	options.report("generating tests")
	payload, _ := json.MarshalIndent(tgInput, "", "  ")
	collected, err := callValidatedToolUse(ctx, provider, options.model("medium"), options.ReasoningEffort, composeSystemPrompt(testGeneratorSystemPrompt), string(payload), options.Images, testGeneratorToolDefinition(len(tgInput.Memory) > 0), options.MaxOutputTokens, &options.Stream, func(collected *zeroruntime.CollectedStream) error {
		_, err := parseTestGeneratorOutput(collected)
		return err
	}, options.PromptCacheKey)
	if err != nil {
		return schemas.HarnessStageOutput{}, withCollectedUsage(err, collected)
	}
	output, err := parseTestGeneratorOutput(collected)
	if err != nil {
		return schemas.HarnessStageOutput{}, withCollectedUsage(err, collected)
	}
	claims, claimIssues := parseDispositionClaims(testGeneratorToolName, collected)
	memoryReview, reviewNote := reconcileMemoryReview(tgInput.Memory, claims, claimIssues)
	if reviewNote != "" {
		options.report(reviewNote)
	}
	output.MemoryDisposition = claims

	changedPaths := make([]string, len(output.Files))
	for i, f := range output.Files {
		changedPaths[i] = f.Path
	}
	options.report("proposed changes: " + formatPathList(changedPaths, 5))

	data := map[string]any{
		"test_generator_input":  tgInput,
		"test_generator_output": output,
	}
	if len(output.Files) > 0 {
		if options.WorkDir == "" {
			return schemas.HarnessStageOutput{}, withCollectedUsage(fmt.Errorf("test generator: WorkDir is required to apply %d file change(s)", len(output.Files)), collected)
		}
		options.report(fmt.Sprintf("applying %d test file change(s)", len(output.Files)))
		apply, err := applyFileChanges(ctx, options.WorkDir, output.Files, options.RunTool)
		if err != nil {
			return schemas.HarnessStageOutput{}, withCollectedUsage(fmt.Errorf("test generator: %w", err), collected)
		}
		if len(apply.Applied) != len(output.Files) {
			return schemas.HarnessStageOutput{}, withCollectedUsage(fmt.Errorf("test generator: applied %d of %d file changes", len(apply.Applied), len(output.Files)), collected)
		}
		options.report(fmt.Sprintf("applied %d test file change(s)", len(apply.Applied)))
		data["file_apply_result"] = apply
	}

	return schemas.HarnessStageOutput{
		Summary:      output.Intent,
		Detail:       strings.Join(changedPaths, ", "),
		Confidence:   output.Confidence,
		MemoryReview: memoryReview,
		Data:         data,
		Usage:        usageFromCollected(collected),
	}, nil
}

func parseTestGeneratorOutput(collected *zeroruntime.CollectedStream) (schemas.TestGeneratorOutput, error) {
	tc := findToolCall(collected, testGeneratorToolName)
	if tc == nil {
		return schemas.TestGeneratorOutput{}, fmt.Errorf("model did not call %s", testGeneratorToolName)
	}
	stripped, err := stripDispositionClaims(tc.Arguments)
	if err != nil {
		return schemas.TestGeneratorOutput{}, fmt.Errorf("parse %s args: %w", testGeneratorToolName, err)
	}
	var output schemas.TestGeneratorOutput
	if err := json.Unmarshal([]byte(stripped), &output); err != nil {
		return schemas.TestGeneratorOutput{}, fmt.Errorf("parse %s args: %w", testGeneratorToolName, err)
	}
	if err := output.Validate(); err != nil {
		return schemas.TestGeneratorOutput{}, err
	}
	return output, nil
}

func testGeneratorToolDefinition(hasMemory bool) zeroruntime.ToolDefinition {
	definition := zeroruntime.ToolDefinition{
		Name:        testGeneratorToolName,
		Description: "Submit the complete TestGeneratorOutput for the requested tests.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"files":             fileChangeArraySchema(),
				"language":          map[string]any{"type": "string"},
				"intent":            map[string]any{"type": "string"},
				"known_limitations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"confidence":        map[string]any{"type": "number"},
			},
			"required": []string{"files", "language", "intent", "confidence"},
		},
	}
	applyMemoryDefinition(definition.Parameters, hasMemory)
	return definition
}
