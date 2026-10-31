package stages

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/errhint"
	"github.com/Taf0711/splice/internal/splice/memoryreason"
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
func composeSystemPrompt(stagePrompt string, projectInstructions ...string) string {
	prompt := spliceOverviewPrompt + "\n\n" + pipelineMetaPrompt + "\n\n" + stagePrompt
	if len(projectInstructions) == 0 || strings.TrimSpace(projectInstructions[0]) == "" {
		return prompt
	}
	return prompt + "\n\nProject instructions (from the repository's instruction files). Follow them for code style and conventions. The pipeline contract and this stage's output format take precedence.\n\n" + strings.TrimSpace(projectInstructions[0])
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
	// changed lists the files the stage changed before it failed. An agent node
	// keeps them, so a failed node's edits still reach the pass evidence.
	changed []string
}

func (e meteredStageError) Error() string                   { return e.err.Error() }
func (e meteredStageError) Unwrap() error                   { return e.err }
func (e meteredStageError) StageUsage() *schemas.StageUsage { return e.usage }

// StageChangedFiles returns the files the failed stage changed, or nil.
func (e meteredStageError) StageChangedFiles() []string { return e.changed }

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
//
// forceChoice, when true, forces the model to call this stage's single typed
// tool (ToolChoice: tool.Name) so a prose answer cannot strand the typed-output
// retry loop. When false the request keeps auto tool-calling behavior. Some
// OpenAI-compatible endpoints reject the forced shape but accept auto calls.
func callToolUse(ctx context.Context, provider zeroruntime.Provider, model, reasoningEffort, systemPrompt, userPrompt string, images []zeroruntime.ImageBlock, tools []zeroruntime.ToolDefinition, maxOutputTokens int, callbacks *zeroruntime.CollectOptions, promptCacheKey string, forceChoice bool) (*zeroruntime.CollectedStream, error) {
	// B4: the request is built by the shared pure builder (one builder for
	// production, dry-run inspection, and tests), and the final gate runs
	// here - after source fulfillment and model-input construction, on
	// EVERY call path including format retries (callValidatedToolUse
	// re-enters callToolUse per attempt). The gate result rides the
	// attempt metadata; a measured overflow is reported through the
	// collect options' activity seam when wired, never silently trimmed.
	request, breakdown := BuildFinalRequest("", model, reasoningEffort, systemPrompt, userPrompt, images, tools, maxOutputTokens, promptCacheKey, forceChoice, 1)
	_ = GateFinalRequest(breakdown, 0) // bound disabled at this layer; stage budgets bound via MaxOutputTokens
	if callbacks != nil && callbacks.OnPromptLayout != nil {
		hash, err := promptLayoutHash(systemPrompt, tools)
		if err != nil {
			return nil, err
		}
		callbacks.OnPromptLayout(hash)
	}
	return streamCompletion(ctx, provider, *request, callbacks)
}

// streamCompletion is the single request gate for stage model calls. Every
// stage request goes through it, so provider handling cannot drift between
// the tool-use path and the prompt path.
func streamCompletion(ctx context.Context, provider zeroruntime.Provider, request zeroruntime.CompletionRequest, callbacks *zeroruntime.CollectOptions) (*zeroruntime.CollectedStream, error) {
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

// callValidatedToolUse retries typed-output contract failures. The observed
// OpenRouter request error gets one compatibility retry with auto tool calling.
// A dropped connection is replayed (transportDropped). All other provider and
// cancellation errors return immediately.
func callValidatedToolUse(ctx context.Context, provider zeroruntime.Provider, model, reasoningEffort, systemPrompt, userPrompt string, images []zeroruntime.ImageBlock, tools []zeroruntime.ToolDefinition, maxOutputTokens int, callbacks *zeroruntime.CollectOptions, validate func(*zeroruntime.CollectedStream) error, promptCacheKey string, onFormatRetry ...func(attempt int)) (*zeroruntime.CollectedStream, error) {
	if len(tools) == 0 {
		return nil, fmt.Errorf("typed tool retry: no tools offered")
	}
	toolNames := make([]string, 0, len(tools))
	for _, t := range tools {
		toolNames = append(toolNames, t.Name)
	}
	toolLabel := strings.Join(toolNames, "+")
	var total zeroruntime.Usage
	attemptPrompt := userPrompt
	var lastErr error
	forceChoice := true
	drops := 0
	for attempt := 1; attempt <= maxTypedToolAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// F1 wiring: attempts 2+ are format_retry spend. The orchestrator's
		// reclassification callback (when supplied) flips the attribution
		// cell BEFORE the request streams, so the per-attempt usage event
		// carries the format_retry source instead of generation.
		if attempt > 1 {
			for _, notify := range onFormatRetry {
				if notify != nil {
					notify(attempt)
				}
			}
		}
		collected, err := callToolUse(ctx, provider, model, reasoningEffort, systemPrompt, attemptPrompt, images, tools, maxOutputTokens, callbacks, promptCacheKey, forceChoice)
		if err != nil {
			if forceChoice && shouldRetryWithAutoToolChoice(err) {
				forceChoice = false
				continue
			}
			if collected != nil {
				addUsage(&total, normalizedAttemptUsage(collected.Usage))
			}
			// The connection dropped: the model's answer never arrived, so the
			// same request is sent again. It is not a format attempt. (P6: a
			// reset mid-repair ended a run that was on course to verify.)
			if transportDropped(ctx, err) && drops < len(transportRetryWaits) {
				wait := transportRetryWaits[drops]
				drops++
				select {
				case <-ctx.Done():
				case <-time.After(wait):
				}
				attempt--
				continue
			}
			if outputCutOff(err, collected) && attempt < maxTypedToolAttempts {
				maxOutputTokens = raisedOutputCap(maxOutputTokens)
				attemptPrompt = userPrompt + outputCutOffFeedback
				continue
			}
			// The ledger already recorded the earlier attempts of this stage
			// call. The error carries their sum, or the run ends on a usage
			// mismatch between the stage and the ledger.
			if collected == nil {
				collected = &zeroruntime.CollectedStream{}
			}
			collected.Usage = total
			return collected, meteredStageError{err: err, usage: usageFromCollected(collected)}
		}
		// Sum the value the request ledger records, not the raw stream usage. The
		// ledger normalizes each attempt via NormalizeUsage and zeroes
		// un-normalizable reports (e.g. reasoning > output, which a provider that
		// reports completion excluding reasoning produces); summing the raw usage
		// here would count an attempt the ledger zeroed, and the stage total would
		// then diverge from the ledger and abort the run at applyRequestLedger.
		addUsage(&total, normalizedAttemptUsage(collected.Usage))
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
				Tool:       toolLabel,
				Attempts:   attempt,
				Cause:      lastErr,
				stageUsage: usageFromCollected(collected),
			}
		}
		if collected.FinishReason == zeroruntime.FinishReasonLength {
			maxOutputTokens = raisedOutputCap(maxOutputTokens)
			attemptPrompt = userPrompt + outputCutOffFeedback
			continue
		}
		feedbackRunes := []rune(lastErr.Error())
		if len(feedbackRunes) > 300 {
			feedbackRunes = feedbackRunes[:300]
		}
		feedback := string(feedbackRunes)
		attemptPrompt = fmt.Sprintf("%s\n\nYour previous response did not satisfy the typed output contract: %s. Call exactly one of %s once with valid JSON arguments matching its schema.", userPrompt, feedback, strings.Join(toolNames, " or "))
	}
	return nil, fmt.Errorf("typed output retry loop ended unexpectedly")
}

// A tool call cut off at the output cap is not a format error: the same cap
// cuts it off again. The retry raises the cap and asks for a smaller
// submission. Some providers report the cutoff as a finish reason, others as
// an HTTP 400 ("Tool calls cutoff by max_tokens").
const (
	maxOutputCapAfterCutOff = 32_768
	outputCutOffFeedback    = "\n\nYour previous response was cut off at the output token limit before the tool call was complete. Submit a smaller call: use edits for existing files instead of their full content, and do not repeat unchanged code."
)

// transportRetryWaits are the waits before each replay of a request whose
// connection dropped; its length bounds the replays.
var transportRetryWaits = []time.Duration{2 * time.Second, 8 * time.Second}

// transportDropped reports a connection failure (reset, EOF, refused,
// timeout, DNS) while the stage's own context is still live. A cancelled or
// expired stage context is never replayed.
func transportDropped(ctx context.Context, err error) bool {
	return err != nil && ctx.Err() == nil && errhint.Classify(err) == errhint.Connectivity
}

func outputCutOff(err error, collected *zeroruntime.CollectedStream) bool {
	if collected != nil && collected.FinishReason == zeroruntime.FinishReasonLength {
		return true
	}
	return err != nil && strings.Contains(err.Error(), "cutoff by max_tokens")
}

// raisedOutputCap doubles a set cap up to the ceiling. Zero (the provider
// default) stays zero: splice does not know the model's own limit.
func raisedOutputCap(current int) int {
	if current <= 0 {
		return current
	}
	return min(current*2, max(current, maxOutputCapAfterCutOff))
}

func shouldRetryWithAutoToolChoice(err error) bool {
	if err == nil {
		return false
	}
	message := strings.TrimSuffix(strings.TrimSpace(err.Error()), ".")
	return strings.HasSuffix(message, "provider request error: Provider returned error")
}

// normalizedAttemptUsage converts one attempt's collected usage into the exact
// value the request ledger records: NormalizeUsage over the canonical fields,
// zeroed when it fails. The request ledger (recordingOptions) applies the same
// policy, so summing this value keeps the stage total and the ledger equal by
// construction. Summing the raw stream usage instead would count an attempt the
// ledger zeroed and abort the run at applyRequestLedger.
func normalizedAttemptUsage(usage zeroruntime.Usage) zeroruntime.Usage {
	normalized, err := zeroruntime.NormalizeUsage(zeroruntime.TokenUsage{
		InputTokens:       usage.InputTokens,
		PromptTokens:      usage.PromptTokens,
		OutputTokens:      usage.OutputTokens,
		CompletionTokens:  usage.CompletionTokens,
		ReasoningTokens:   usage.ReasoningTokens,
		CachedInputTokens: usage.CachedInputTokens,
		CacheWriteTokens:  usage.CacheWriteTokens,
		WebSearchRequests: usage.WebSearchRequests,
		WebSearchEngine:   usage.WebSearchEngine,
	})
	if err != nil {
		return zeroruntime.Usage{}
	}
	return normalized
}

func addUsage(total *zeroruntime.Usage, current zeroruntime.Usage) {
	// Sum the EFFECTIVE canonical fields only. Input/Output are the normalized
	// aliases of Prompt/Completion (NormalizeUsage sets both), so summing both
	// pairs would double-count; and when an attempt's usage survives un-normalized
	// (mergeUsageSnapshot's fallback on a reasoning>output report), only the raw
	// Prompt/Completion fields are set, so the effective accessors are the only
	// source that is always right. This keeps the summed total byte-consistent
	// with the per-stream ledger, which also sums Effective* fields.
	total.InputTokens += current.EffectiveInputTokens()
	total.OutputTokens += current.EffectiveOutputTokens()
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
// typed StageUsage for the orchestrator ledger. Returns nil when no token or
// web-search usage was reported, so nil-memory runs stay byte-identical and
// the StageRecord keeps zero fields.
func usageFromCollected(collected *zeroruntime.CollectedStream) *schemas.StageUsage {
	if collected == nil {
		return nil
	}
	u := collected.Usage
	if u.InputTokens == 0 && u.OutputTokens == 0 && u.WebSearchRequests == 0 {
		return nil
	}
	return &schemas.StageUsage{
		InputTokens:       u.InputTokens,
		OutputTokens:      u.OutputTokens,
		CachedInputTokens: u.CachedInputTokens,
		CacheWriteTokens:  u.CacheWriteTokens,
		ReasoningTokens:   u.ReasoningTokens,
		WebSearchRequests: u.WebSearchRequests,
		WebSearchEngine:   u.WebSearchEngine,
	}
}

var memoryDispositionSchema = map[string]any{
	"type": "array",
	"items": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"memory_id": map[string]any{"type": "string"},
			"action":    map[string]any{"type": "string", "enum": []string{schemas.MemoryActionApplied, schemas.MemoryActionRejected}},
			"reason":    map[string]any{"type": "string", "enum": []string{schemas.MemoryReasonRelevant, schemas.MemoryReasonIrrelevant, schemas.MemoryReasonStaleOrIncompatible, schemas.MemoryReasonContradicted}},
		},
		"required": []string{"memory_id", "action", "reason"},
	},
}

// applyMemoryDefinition adds the memory_disposition property to a tool schema.
// The property is never added to required, so the schema stays byte-identical
// whether or not memory was delivered. A memory-dependent schema changes the
// cached prompt prefix between rounds of the same stage and forfeits the
// provider's prefix cache. Presence is enforced in code instead:
// reconcileMemoryReview marks a delivered item that carries no valid claim as
// unreported.
func applyMemoryDefinition(params map[string]any) {
	props := params["properties"].(map[string]any)
	props["memory_disposition"] = memoryDispositionSchema
}

// promptLayoutHash returns a stable hash of the cacheable prompt prefix: the
// system prompt and every tool definition, in order. The prefix must not
// depend on per-request state such as memory presence, because any change
// invalidates the provider's prefix cache for later rounds of the same stage.
// Callers record the hash per request so a flip between rounds is visible in
// the run ledger. Canonical JSON gives a stable byte sequence for each schema.
func promptLayoutHash(systemPrompt string, tools []zeroruntime.ToolDefinition) (string, error) {
	sum := sha256.New()
	fmt.Fprintf(sum, "system\x00%d\x00%s", len(systemPrompt), systemPrompt)
	for _, tool := range tools {
		schema, err := json.Marshal(tool.Parameters)
		if err != nil {
			return "", fmt.Errorf("layout hash: marshal tool schema for %q: %w", tool.Name, err)
		}
		fmt.Fprintf(sum, "tool\x00%d\x00%s\x00%d\x00%s\x00%d\x00%s", len(tool.Name), tool.Name, len(tool.Description), tool.Description, len(schema), schema)
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// stripDispositionClaims removes the memory_disposition property from a tool
// call's arguments before core decoding, so malformed bookkeeping JSON can
// never fail typed-output validation and trigger a provider retry.
func stripDispositionClaims(args string) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &fields); err != nil {
		return "", err
	}
	delete(fields, "memory_disposition")
	stripped, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	return string(stripped), nil
}

// parseDispositionClaims decodes memory_disposition as raw claims plus an
// issue count: a missing array, wrong shape, or malformed item each counts as
// one issue and never invalidates the substantive output.
func parseDispositionClaims(toolName string, collected *zeroruntime.CollectedStream) ([]schemas.MemoryDisposition, int) {
	tc := findToolCall(collected, toolName)
	if tc == nil {
		return nil, 0
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(tc.Arguments), &fields); err != nil {
		return nil, 1
	}
	rawDisposition, ok := fields["memory_disposition"]
	if !ok || string(rawDisposition) == "null" {
		return nil, 1
	}
	var rawClaims []json.RawMessage
	if err := json.Unmarshal(rawDisposition, &rawClaims); err != nil {
		return nil, 1
	}
	claims := make([]schemas.MemoryDisposition, 0, len(rawClaims))
	issues := 0
	for _, raw := range rawClaims {
		var claim schemas.MemoryDisposition
		if err := json.Unmarshal(raw, &claim); err != nil || claim.MemoryID == "" {
			issues++
			continue
		}
		claims = append(claims, claim)
	}
	return claims, issues
}

// reconcileMemoryReview normalizes model claims into the complete per-
// invocation review and renders at most one progress note when bookkeeping
// was incomplete. It returns a nil review when no memory was delivered.
func reconcileMemoryReview(delivered []schemas.SelectedMemory, claims []schemas.MemoryDisposition, parseIssues int) (*schemas.MemoryReview, string) {
	review := memoryreason.Reconcile(delivered, claims, parseIssues)
	if review == nil {
		return nil, ""
	}
	unreported := 0
	for _, item := range review.Items {
		if item.Action == schemas.MemoryActionUnreported {
			unreported++
		}
	}
	switch {
	case review.InvalidClaims > 0 && unreported > 0:
		return review, fmt.Sprintf("memory dispositions: %d invalid claim(s); %d delivered item(s) unreported", review.InvalidClaims, unreported)
	case review.InvalidClaims > 0:
		return review, fmt.Sprintf("memory dispositions: %d invalid claim(s)", review.InvalidClaims)
	case unreported > 0:
		return review, fmt.Sprintf("memory dispositions: %d delivered item(s) unreported", unreported)
	default:
		return review, ""
	}
}
