package stages

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	// B3: the post-write source request precedes the default request. The
	// writer's changed files are the FIRST thing test generation needs;
	// when they exist, the default discovery request would fetch
	// pre-write bytes and the generator would target symbols that no
	// longer match the tree.
	writerChangedPathsEarly := append([]string(nil), input.PriorChangedFiles["code_writer"]...)
	if input.Context == nil && len(writerChangedPathsEarly) > 0 && options.PullContext {
		// Existing sibling tests are edit targets too: without their actual
		// bytes the model treats the file as absent and submits create,
		// which fails against an already-existing test file. Request the
		// post-write sources first, then the existing tests they own.
		requestPaths := append([]string(nil), writerChangedPathsEarly...)
		requestPaths = append(requestPaths, relatedExistingTestPaths(options.WorkDir, writerChangedPathsEarly)...)
		queries := make([]schemas.ContextQuery, 0, len(requestPaths))
		for _, path := range requestPaths {
			p := path
			queries = append(queries, schemas.ContextQuery{
				QueryType:  schemas.ContextReadFile,
				Path:       &p,
				MaxResults: 10,
				MaxChars:   12000,
			})
		}
		options.report(fmt.Sprintf("requesting post-write source for %d changed file(s)", len(queries)))
		return schemas.HarnessStageOutput{
			Summary:    "Test Generator requested post-write implementation source.",
			Detail:     "The writer changed files this run; tests are generated against the post-write bytes.",
			Confidence: 1.0,
			ContextRequest: &schemas.ContextRequest{
				Reason:  "post-write implementation source for test generation",
				Queries: queries,
			},
		}, nil
	}
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
	// B3: the fulfilled context bundle's source evidence must actually
	// reach the provider. The relevantContext array carries it through
	// formatContextBundle via selectRelevantContext; the historical bug
	// was constructing RelevantContext WITHOUT the bundle, so fetched
	// source never entered the payload. The code_writer summary stays a
	// one-line pointer; it must not substitute for the bytes.
	relevantContext = append(relevantContext, selectRelevantContext(nil, nil, input.Context, input.PipelineStages)...)
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

// relatedExistingTestPaths lists sibling test files that already exist for
// the writer's changed implementation files. They are the files a regression
// edit must modify rather than recreate.
func relatedExistingTestPaths(workDir string, writerPaths []string) []string {
	if workDir == "" || len(writerPaths) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range writerPaths {
		p = strings.TrimSpace(p)
		if p == "" || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		candidate := strings.TrimSuffix(p, ".go") + "_test.go"
		if seen[candidate] {
			continue
		}
		if _, err := os.Stat(filepath.Join(workDir, filepath.FromSlash(candidate))); err != nil {
			continue
		}
		seen[candidate] = true
		out = append(out, candidate)
	}
	return out
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
	// C3: both protocol versions, normalized through the SAME shared
	// materializer as the writer (parseCodeWriterArgs' logic, typed for
	// this output).
	var probe struct {
		Files []json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal([]byte(stripped), &probe); err != nil {
		return schemas.TestGeneratorOutput{}, fmt.Errorf("parse %s args: %w", testGeneratorToolName, err)
	}
	var output schemas.TestGeneratorOutput
	if err := json.Unmarshal([]byte(stripped), &output); err != nil {
		return schemas.TestGeneratorOutput{}, fmt.Errorf("parse %s args: %w", testGeneratorToolName, err)
	}
	if proposalsContainEdits(probe.Files) {
		proposals, derr := decodeProposals(probe.Files)
		if derr != nil {
			return schemas.TestGeneratorOutput{}, derr
		}
		changes, _, merr := MaterializeProposals(proposals, currentProposalSnapshot)
		if merr != nil {
			return schemas.TestGeneratorOutput{}, fmt.Errorf("normalize compact proposals: %w", merr)
		}
		output.Files = changes
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
				"files":             proposalArraySchema(),
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
