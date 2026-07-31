package stages

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

//go:embed prompts/pipeline_meta.md
var pipelineMetaPrompt string

//go:embed prompts/splice_overview.md
var spliceOverviewPrompt string

// SpliceOverviewPrompt is the phase-level framing shared by every model-backed
// agent, including the design conversation. It deliberately excludes the
// execution-phase typed input/output contract, which does not apply to a
// free-form conversation with real read tools.
func SpliceOverviewPrompt() string {
	return spliceOverviewPrompt
}

// composeSystemPrompt builds an execution-phase stage's system prompt: the
// shared overview, then the pipeline contract, then the stage's own prompt.
func composeSystemPrompt(stagePrompt string) string {
	return spliceOverviewPrompt + "\n\n" + pipelineMetaPrompt + "\n\n" + stagePrompt
}

const maxTypedToolAttempts = 3

// TypedOutputError reports exhausted typed-tool retries. StageUsage preserves
// usage from every completed attempt so failed local-model calls remain visible
// in the pipeline ledger.
type TypedOutputError struct {
	Model      string
	Tool       string
	Attempts   int
	Cause      error
	stageUsage *schemas.StageUsage
}

func (e *TypedOutputError) Error() string {
	return fmt.Sprintf("model %q failed typed output after %d attempts: required tool %q with valid JSON arguments: %v; select a model that supports tool calling through the configured API (OpenAI-compatible for local runtimes)", e.Model, e.Attempts, e.Tool, e.Cause)
}

func (e *TypedOutputError) Unwrap() error { return e.Cause }

// StageUsage returns usage accumulated across every completed retry attempt.
func (e *TypedOutputError) StageUsage() *schemas.StageUsage { return e.stageUsage }

type meteredStageError struct {
	err   error
	usage *schemas.StageUsage
}

func (e meteredStageError) Error() string                   { return e.err.Error() }
func (e meteredStageError) Unwrap() error                   { return e.err }
func (e meteredStageError) StageUsage() *schemas.StageUsage { return e.usage }

func withCollectedUsage(err error, collected *zeroruntime.CollectedStream) error {
	if err == nil || collected == nil {
		return err
	}
	var metered interface{ StageUsage() *schemas.StageUsage }
	if errors.As(err, &metered) {
		return err
	}
	return meteredStageError{err: err, usage: usageFromCollected(collected)}
}

// callToolUse runs a single-turn tool-use completion and returns the collected stream.
// If callbacks are provided, text and tool-call events are forwarded.
func callToolUse(ctx context.Context, provider zeroruntime.Provider, model, reasoningEffort, systemPrompt, userPrompt string, images []zeroruntime.ImageBlock, tool zeroruntime.ToolDefinition, callbacks *zeroruntime.CollectOptions, promptCacheKey string) (*zeroruntime.CollectedStream, error) {
	messages := []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleSystem, Content: systemPrompt},
		{Role: zeroruntime.MessageRoleUser, Content: userPrompt, Images: images},
	}
	request := zeroruntime.CompletionRequest{
		Messages:        messages,
		Tools:           []zeroruntime.ToolDefinition{tool},
		ReasoningEffort: reasoningEffort,
		PromptCacheKey:  promptCacheKey,
		// Force the model to call this stage's single typed tool so a prose
		// answer cannot strand the typed-output retry loop. Empty keeps auto
		// behavior; callToolUse always passes exactly one tool, so forcing its
		// name is always correct here.
		ToolChoice: tool.Name,
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

// callValidatedToolUse retries only typed-output contract failures. Transport,
// stream, and cancellation errors return immediately. The original typed input
// is repeated with bounded corrective feedback because local OpenAI-compatible
// models often recover when the required tool and JSON defect are explicit.
func callValidatedToolUse(ctx context.Context, provider zeroruntime.Provider, model, reasoningEffort, systemPrompt, userPrompt string, images []zeroruntime.ImageBlock, tool zeroruntime.ToolDefinition, callbacks *zeroruntime.CollectOptions, validate func(*zeroruntime.CollectedStream) error, promptCacheKey string) (*zeroruntime.CollectedStream, error) {
	var total zeroruntime.Usage
	attemptPrompt := userPrompt
	var lastErr error
	for attempt := 1; attempt <= maxTypedToolAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		collected, err := callToolUse(ctx, provider, model, reasoningEffort, systemPrompt, attemptPrompt, images, tool, callbacks, promptCacheKey)
		if err != nil {
			return collected, err
		}
		addUsage(&total, collected.Usage)
		if err := validate(collected); err == nil {
			collected.Usage = total
			return collected, nil
		} else {
			lastErr = err
		}
		if attempt == maxTypedToolAttempts {
			collected.Usage = total
			return collected, &TypedOutputError{
				Model:      model,
				Tool:       tool.Name,
				Attempts:   attempt,
				Cause:      lastErr,
				stageUsage: usageFromCollected(collected),
			}
		}
		feedbackRunes := []rune(lastErr.Error())
		if len(feedbackRunes) > 300 {
			feedbackRunes = feedbackRunes[:300]
		}
		feedback := string(feedbackRunes)
		attemptPrompt = fmt.Sprintf("%s\n\nYour previous response did not satisfy the typed output contract: %s. Call %s exactly once with valid JSON arguments matching its schema.", userPrompt, feedback, tool.Name)
	}
	return nil, fmt.Errorf("typed output retry loop ended unexpectedly")
}

func addUsage(total *zeroruntime.Usage, current zeroruntime.Usage) {
	total.InputTokens += current.InputTokens
	total.OutputTokens += current.OutputTokens
	total.PromptTokens += current.PromptTokens
	total.CompletionTokens += current.CompletionTokens
	total.CachedInputTokens += current.CachedInputTokens
	total.CacheWriteTokens += current.CacheWriteTokens
	total.ReasoningTokens += current.ReasoningTokens
	total.WebSearchRequests += current.WebSearchRequests
}

func findToolCall(collected *zeroruntime.CollectedStream, name string) *zeroruntime.ToolCall {
	for i := range collected.ToolCalls {
		if collected.ToolCalls[i].Name == name {
			return &collected.ToolCalls[i]
		}
	}
	return nil
}

// usageFromCollected converts the provider stream's normalized Usage into a
// typed StageUsage for the orchestrator ledger. Returns nil when no real
// usage was reported (both input and output are zero), so nil-memory runs
// stay byte-identical and the StageRecord keeps zero fields.
func usageFromCollected(collected *zeroruntime.CollectedStream) *schemas.StageUsage {
	if collected == nil {
		return nil
	}
	u := collected.Usage
	if u.InputTokens == 0 && u.OutputTokens == 0 {
		return nil
	}
	return &schemas.StageUsage{
		InputTokens:       u.InputTokens,
		OutputTokens:      u.OutputTokens,
		CachedInputTokens: u.CachedInputTokens,
		CacheWriteTokens:  u.CacheWriteTokens,
		ReasoningTokens:   u.ReasoningTokens,
	}
}
