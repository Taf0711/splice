package stages

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// Bounds for a prompt node's bounded context and output. A prompt node is the
// one custom node type that reaches a model, so every input and output is
// capped here rather than trusted.
const (
	maxPromptSummaryChars = 2000
	maxPromptMemoryChars  = 2000
	maxPromptOutputChars  = 4000
)

// promptStageSystemPrompt frames a prompt node as a single bounded answer with
// no tools. The rendered template is the user message; its context variables
// are delimited data, never instructions.
const promptStageSystemPrompt = "You are one stage in a deterministic coding pipeline. " +
	"Answer the instruction with a concise, self-contained result. " +
	"You have no tools. Your answer is a hand-off summary for the next stage, so state what you determined or produced, not the steps you took."

// PromptStage runs one topology prompt node: a single model call with a
// bounded, documented context. It has no tool access in v1, so a prompt node
// cannot mutate the workspace. Its template may reference {{intent}},
// {{summaries}} (the edge-scoped dependency summaries), and {{memory}} (the
// delivered memory bundle).
type PromptStage struct {
	// Name is the node name; it labels errors and activity.
	Name string
	// Template is the node's inline prompt template.
	Template string
}

var _ Stage = PromptStage{}

// Capabilities reports a prompt node as model-backed with no context or
// memory pull by default. A topology overrides these through node
// capabilities.
func (s PromptStage) Capabilities() Capabilities {
	return Capabilities{ModelFree: false, PullContext: false, ConsumesMemory: false, Description: "running prompt " + s.Name}
}

// Run renders the template, makes one model call, and returns the bounded
// answer as the stage output. It fails loud on an empty template, a missing
// provider, or an empty answer.
func (s PromptStage) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options StageOptions) (schemas.HarnessStageOutput, error) {
	if strings.TrimSpace(s.Template) == "" {
		return schemas.HarnessStageOutput{}, fmt.Errorf("prompt node %s: template is empty", s.Name)
	}
	if provider == nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("prompt node %s: provider is required", s.Name)
	}
	options.report("running prompt " + s.Name)
	rendered := renderPromptTemplate(s.Template, input)
	collected, err := callTextCompletion(ctx, provider, options.model("medium"), options.ReasoningEffort, composeSystemPrompt(promptStageSystemPrompt), rendered, options.Images, options.MaxOutputTokens, &options.Stream, options.PromptCacheKey)
	if err != nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("prompt node %s: %w", s.Name, err)
	}
	answer := strings.TrimSpace(collected.Text)
	if answer == "" {
		return schemas.HarnessStageOutput{}, fmt.Errorf("prompt node %s: model returned no output", s.Name)
	}
	output := truncatePromptOutput(answer)
	return schemas.HarnessStageOutput{
		Summary:    promptFirstLine(output),
		Detail:     output,
		Confidence: 0.5,
		Data: map[string]any{
			"prompt_output": output,
		},
		Usage: usageFromCollected(collected),
	}, nil
}

// callTextCompletion runs one plain completion with no tools, so a prompt node
// cannot reach the workspace through a tool call.
func callTextCompletion(ctx context.Context, provider zeroruntime.Provider, model, reasoningEffort, systemPrompt, userPrompt string, images []zeroruntime.ImageBlock, maxOutputTokens int, callbacks *zeroruntime.CollectOptions, promptCacheKey string) (*zeroruntime.CollectedStream, error) {
	messages := []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleSystem, Content: systemPrompt},
		{Role: zeroruntime.MessageRoleUser, Content: userPrompt, Images: images},
	}
	request := zeroruntime.CompletionRequest{
		Messages:        messages,
		ReasoningEffort: reasoningEffort,
		PromptCacheKey:  promptCacheKey,
		MaxOutputTokens: maxOutputTokens,
	}
	events, err := provider.StreamCompletion(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("stream completion: %w", err)
	}
	var opts zeroruntime.CollectOptions
	if callbacks != nil {
		opts = *callbacks
	}
	collected := zeroruntime.CollectStreamWithOptions(ctx, events, opts)
	if collected.Error != "" {
		return &collected, fmt.Errorf("stream error: %s", collected.Error)
	}
	return &collected, nil
}

// renderPromptTemplate substitutes the three documented context variables. An
// unknown {{name}} is left as written, so a typo is visible rather than
// silently empty.
func renderPromptTemplate(template string, input schemas.HarnessStageInput) string {
	out := strings.ReplaceAll(template, "{{intent}}", input.RequestIntent)
	out = strings.ReplaceAll(out, "{{summaries}}", renderPromptSummaries(input.PriorSummaries))
	out = strings.ReplaceAll(out, "{{memory}}", renderPromptMemory(input.MemoryBundle))
	return out
}

// renderPromptSummaries renders the edge-scoped dependency summaries in a
// stable order, bounded to maxPromptSummaryChars.
func renderPromptSummaries(summaries map[string]string) string {
	if len(summaries) == 0 {
		return ""
	}
	names := make([]string, 0, len(summaries))
	for name := range summaries {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(summaries[name])
		b.WriteString("\n")
	}
	return truncatePromptContext(b.String(), maxPromptSummaryChars)
}

// renderPromptMemory renders the delivered memory bundle, bounded to
// maxPromptMemoryChars. A nil bundle renders nothing.
func renderPromptMemory(bundle *schemas.MemoryBundle) string {
	if bundle == nil || (len(bundle.Observations) == 0 && len(bundle.Exemplars) == 0) {
		return ""
	}
	var b strings.Builder
	for _, observation := range bundle.Observations {
		b.WriteString("- ")
		if observation.Title != "" {
			b.WriteString(observation.Title)
			b.WriteString(": ")
		}
		b.WriteString(observation.Content)
		b.WriteString("\n")
	}
	for _, exemplar := range bundle.Exemplars {
		b.WriteString("- example: ")
		b.WriteString(exemplar.Content)
		b.WriteString("\n")
	}
	return truncatePromptContext(b.String(), maxPromptMemoryChars)
}

func truncatePromptContext(value string, limit int) string {
	return truncateRunes(value, limit)
}

func truncatePromptOutput(value string) string {
	return truncateRunes(value, maxPromptOutputChars)
}

// promptFirstLine returns the first non-empty line, bounded, as the summary.
func promptFirstLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return truncateRunes(line, 200)
		}
	}
	return value
}

// truncateRunes returns value truncated to at most limit characters, counting
// runes so the result stays valid UTF-8.
func truncateRunes(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
