package openai

import "encoding/json"

type chatCompletionRequest struct {
	Model               string            `json:"model"`
	Messages            []chatMessage     `json:"messages"`
	Tools               []toolDefinition  `json:"tools,omitempty"`
	MaxCompletionTokens int               `json:"max_completion_tokens,omitempty"`
	ReasoningEffort     string            `json:"reasoning_effort,omitempty"`
	Reasoning           *reasoningOptions `json:"reasoning,omitempty"`
	Stream              bool              `json:"stream"`
	StreamOptions       *streamOptions    `json:"stream_options,omitempty"`
	// PromptCacheKey asks the backend to route the request to a replica that
	// already holds this conversation's prefix in its prompt cache (the OpenAI
	// `prompt_cache_key` parameter). Omitted when the caller carries no session
	// identity or when SPLICE_DISABLE_PROMPT_CACHE_KEY is set.
	PromptCacheKey string `json:"prompt_cache_key,omitempty"`
	// ToolChoice, when non-nil, forces the model to call one specific function
	// this request (forced tool use). The mapper builds the native
	// {"type":"function","function":{"name":<ToolChoice>}} object. Nil keeps
	// the current auto behavior byte-for-byte (omitted via omitempty).
	ToolChoice any `json:"tool_choice,omitempty"`
}

// streamOptions requests the final usage chunk on a streaming response. Without
// include_usage the OpenAI streaming API never sends the usage object, so token
// accounting is silently splice for real OpenAI streams.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type reasoningOptions struct {
	Effort string `json:"effort"`
}

type chatMessage struct {
	Role string `json:"role"`
	// Content has no omitempty: strict OpenAI-compatible servers (e.g. some
	// Ollama-cloud models like glm-*) reject a message whose `content` is absent
	// or null with "invalid message content type: <nil>". mapMessage always sets
	// this (to "" when there's no text), so a contentless message serializes as
	// `"content":""` rather than being dropped.
	Content          any               `json:"content"`
	ToolCalls        []requestToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	ReasoningDetails []json.RawMessage `json:"reasoning_details,omitempty"`
}

// contentPart is one element of an OpenAI multimodal `content` array. A part is
// either text (Type "text") or an inline image data URI (Type "image_url").
type contentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *imageURLPart `json:"image_url,omitempty"`
}

// imageURLPart carries an inline image as a `data:<media>;base64,<b64>` URI.
type imageURLPart struct {
	URL string `json:"url"`
}

type requestToolCall struct {
	ID       string                  `json:"id"`
	Type     string                  `json:"type"`
	Function requestToolCallFunction `json:"function"`
}

type requestToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolDefinition struct {
	Type       string         `json:"type"`
	Function   toolFunction   `json:"-"`
	Parameters map[string]any `json:"-"`
}

// MarshalJSON supports function tools and OpenRouter server tools.
func (tool toolDefinition) MarshalJSON() ([]byte, error) {
	if tool.Type == "function" {
		return json.Marshal(struct {
			Type     string       `json:"type"`
			Function toolFunction `json:"function"`
		}{Type: tool.Type, Function: tool.Function})
	}
	return json.Marshal(struct {
		Type       string         `json:"type"`
		Parameters map[string]any `json:"parameters"`
	}{Type: tool.Type, Parameters: tool.Parameters})
}

type toolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type streamChunk struct {
	Choices []streamChoice `json:"choices"`
	Usage   *usage         `json:"usage"`
	Error   *apiError      `json:"error"`
}

type streamChoice struct {
	Delta        streamDelta    `json:"delta"`
	Message      *streamMessage `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

type streamMessage struct {
	Annotations      []streamAnnotation `json:"annotations"`
	ReasoningDetails []reasoningDetail  `json:"reasoning_details"`
}

type streamDelta struct {
	Content          string                `json:"content"`
	ReasoningContent string                `json:"reasoning_content"`
	Reasoning        string                `json:"reasoning"`
	ReasoningDetails []reasoningDetail     `json:"reasoning_details"`
	ToolCalls        []streamToolCallDelta `json:"tool_calls"`
	Annotations      []streamAnnotation    `json:"annotations"`
}

// reasoningDetail is one OpenRouter structured reasoning entry. Raw preserves
// signatures and opaque fields so tool-call continuations can replay it exactly.
type reasoningDetail struct {
	ID        *string         `json:"id"`
	Type      string          `json:"type"`
	Format    string          `json:"format"`
	Index     *int            `json:"index"`
	Text      string          `json:"text"`
	Summary   string          `json:"summary"`
	Signature string          `json:"signature"`
	Data      string          `json:"data"`
	Raw       json.RawMessage `json:"-"`
}

func (detail *reasoningDetail) UnmarshalJSON(data []byte) error {
	type wire reasoningDetail
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*detail = reasoningDetail(decoded)
	detail.Raw = append(detail.Raw[:0], data...)
	return nil
}

type streamToolCallDelta struct {
	Index    int                 `json:"index"`
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function streamFunctionDelta `json:"function"`
}

type streamFunctionDelta struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type streamAnnotation struct {
	Type        string             `json:"type"`
	URL         string             `json:"url"`
	Title       string             `json:"title"`
	URLCitation *streamURLCitation `json:"url_citation"`
}

type streamURLCitation struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

type usage struct {
	PromptTokens            int                    `json:"prompt_tokens"`
	CompletionTokens        int                    `json:"completion_tokens"`
	PromptTokensDetails     promptTokenDetails     `json:"prompt_tokens_details"`
	CompletionTokensDetails completionTokenDetails `json:"completion_tokens_details"`
	// Cost is OpenRouter's exact billed charge for this request in USD. Absent
	// (nil) on every other OpenAI-compatible backend. Only trusted when the
	// request went to openrouter.ai (see Provider.isOpenRouter) — the field
	// name is an OpenRouter extension, not part of the OpenAI schema, so an
	// unrelated proxy reusing it could mean something else entirely.
	Cost *float64 `json:"cost,omitempty"`
}

type promptTokenDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type completionTokenDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}
