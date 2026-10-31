package stages

// Work package B4 (warm-cost handoff Section 6): the final-request gate
// and the shared pure request builder.
//
// One builder constructs the actual provider request for production, the
// dry-run inspection seam, and tests. There is no second approximate
// builder. The final gate measures the request AFTER source fulfillment
// and model-input construction - system prompt, tool schema, user payload
// (source views, memory, summaries, repair evidence) - and applies to
// every call path that goes through the builder, which includes format
// retries (callValidatedToolUse re-invokes the builder per attempt).
//
// Component byte counts are exact (Go string length). Token estimates are
// deterministic byte/4 figures, explicitly labeled estimates; actual
// provider usage stays authoritative.

import (
	"encoding/json"
	"fmt"

	"github.com/Taf0711/splice/internal/zeroruntime"
)

// FinalRequestBreakdown is the measured composition of one provider
// request. Byte counts are exact over the serialized request; token
// estimates carry the estimate label in the field comment and in the
// JSON keys ("estimate").
type FinalRequestBreakdown struct {
	// Exact byte counts over the request's serialized components.
	SystemBytes    int `json:"system_bytes"`
	SchemaBytes    int `json:"schema_bytes"`
	UserBytes      int `json:"user_bytes"`
	TotalBytes     int `json:"total_bytes"`
	MaxOutputBound int `json:"max_output_tokens,omitempty"`

	// Deterministic ESTIMATES (bytes/4). Never a hard upper bound;
	// tokenization across concatenated boundaries is not additive.
	EstimatedInputTokens int `json:"estimated_input_tokens"`

	// Component provenance: which stage/attempt built this request, so a
	// breakdown can be attributed in traces. Attempt 1 is the primary
	// call; 2+ are format retries.
	Stage   string `json:"stage,omitempty"`
	Attempt int    `json:"attempt,omitempty"`

	// EstimateLabel makes the estimate honest in serialized form.
	EstimateLabel string `json:"estimate_label"`
}

// EstimateLabelEstimate is the constant provenance label for token fields.
const EstimateLabelEstimate = "bytes/4 estimate; provider usage is authoritative"

// BuildFinalRequest is the shared pure request builder. Production, the
// dry-run inspection seam, and tests all call this one function, so what
// the gate measures is byte-identical to what the provider receives.
//
// tools carries every tool the model may call this request. With one tool
// and forceChoice, the request forces that exact tool (the historical
// single-typed-tool behavior). With several tools and forceChoice, the
// request requires some tool call without naming which, so a stage that
// accepts more than one typed action stays reachable; a provider that
// cannot express any-tool forcing degrades through the established
// provider-error fallback.
func BuildFinalRequest(stage, model, reasoningEffort, systemPrompt, userPrompt string, images []zeroruntime.ImageBlock, tools []zeroruntime.ToolDefinition, maxOutputTokens int, promptCacheKey string, forceChoice bool, attempt int) (*zeroruntime.CompletionRequest, FinalRequestBreakdown) {
	messages := []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleSystem, Content: systemPrompt},
		{Role: zeroruntime.MessageRoleUser, Content: userPrompt, Images: images},
	}
	request := &zeroruntime.CompletionRequest{
		Messages:        messages,
		Tools:           append([]zeroruntime.ToolDefinition(nil), tools...),
		ReasoningEffort: reasoningEffort,
		PromptCacheKey:  promptCacheKey,
		MaxOutputTokens: maxOutputTokens,
	}
	if forceChoice {
		if len(tools) == 1 {
			request.ToolChoice = tools[0].Name
		} else if len(tools) > 1 {
			request.ToolChoiceRequired = true
		}
	}
	systemBytes := len(systemPrompt)
	schemaBytes := 0
	for _, tool := range tools {
		encoded, err := json.Marshal(tool)
		if err != nil {
			schemaBytes += len(tool.Name)
			continue
		}
		schemaBytes += len(encoded)
	}
	userBytes := len(userPrompt)
	breakdown := FinalRequestBreakdown{
		SystemBytes:    systemBytes,
		SchemaBytes:    schemaBytes,
		UserBytes:      userBytes,
		TotalBytes:     systemBytes + schemaBytes + userBytes,
		MaxOutputBound: maxOutputTokens,
		// Deterministic estimate over the exact bytes. bytes/4 is the
		// project's standing estimate ratio (bytesPerTokenEstimate).
		EstimatedInputTokens: (systemBytes + schemaBytes + userBytes + 3) / 4,
		Stage:                stage,
		Attempt:              attempt,
		EstimateLabel:        EstimateLabelEstimate,
	}
	return request, breakdown
}

// FinalRequestGate is the measured gate result for one request.
type FinalRequestGate struct {
	Breakdown FinalRequestBreakdown
	// Overflow is non-nil when the estimated request exceeds the bound.
	// It names the bound and the measured size; the caller decides the
	// response (targeted ranges, narrower operation, or an explicit
	// incomplete outcome). Compaction decisions live with the caller:
	// the gate measures, it does not silently trim required content.
	Overflow *FinalRequestOverflow
}

// FinalRequestOverflow names a measured overflow.
type FinalRequestOverflow struct {
	BoundBytes    int
	MeasuredBytes int
	Message       string
}

func (e *FinalRequestOverflow) Error() string { return e.Message }

// GateFinalRequest checks the measured request against the input bound.
// boundInputTokens <= 0 disables the gate (no bound configured). The
// estimate is the gate's planning signal; actual provider usage remains
// authoritative for verdicts.
func GateFinalRequest(breakdown FinalRequestBreakdown, boundInputTokens int) FinalRequestGate {
	gate := FinalRequestGate{Breakdown: breakdown}
	if boundInputTokens <= 0 {
		return gate
	}
	if breakdown.EstimatedInputTokens > boundInputTokens {
		gate.Overflow = &FinalRequestOverflow{
			BoundBytes:    boundInputTokens * 4,
			MeasuredBytes: breakdown.TotalBytes,
			Message: fmt.Sprintf("final request estimate %d tokens (bytes/4) exceeds the %d-token input bound (%d bytes measured); targeted ranges, a narrower operation, or an explicit incomplete outcome is required",
				breakdown.EstimatedInputTokens, boundInputTokens, breakdown.TotalBytes),
		}
	}
	return gate
}
