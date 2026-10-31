// Package llmdecisions obtains decision probabilities through a provider tool.
package llmdecisions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/providers/httpdecisions"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// Options sets the model, body limits, and timeout.
type Options struct {
	Model                             string
	MaxRequestBytes, MaxResponseBytes int
	Timeout                           time.Duration
	// ReasoningEffort, when non-empty, is sent with each decision request.
	ReasoningEffort string
}

// Client obtains decision probabilities through a provider tool.
type Client struct {
	provider zeroruntime.Provider
	options  Options
}

// NewClient applies default limits and creates a provider decision client.
func NewClient(provider zeroruntime.Provider, o Options) *Client {
	if o.MaxRequestBytes == 0 {
		o.MaxRequestBytes = 16384
	}
	if o.MaxResponseBytes == 0 {
		o.MaxResponseBytes = 65536
	}
	if o.Timeout == 0 {
		o.Timeout = 1500 * time.Millisecond
	}
	return &Client{provider: provider, options: o}
}

// Evaluate returns probabilities stated by the model through one answer tool.
// Calibrate the thresholds for each backend.
func (c *Client) Evaluate(ctx context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
	if c == nil || c.provider == nil {
		return zeroruntime.DecisionResult{}, fmt.Errorf("provider decisions: evaluator is not wired")
	}
	if err := req.Validate(); err != nil {
		return zeroruntime.DecisionResult{}, err
	}
	body, err := httpdecisions.EncodeRequest(req)
	if err != nil {
		return zeroruntime.DecisionResult{}, err
	}
	answerSchema := map[string]any{
		"type":     "object",
		"required": []string{"answers"},
		"properties": map[string]any{
			"answers": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"question_id", "probabilities"},
					"properties": map[string]any{
						"question_id": map[string]any{"type": "string"},
						"probabilities": map[string]any{
							"type": "object",
							"additionalProperties": map[string]any{
								"type":    "number",
								"minimum": 0,
								"maximum": 1,
							},
						},
					},
				},
			},
		},
	}
	completion := zeroruntime.CompletionRequest{
		ToolChoice:      "answer",
		MaxOutputTokens: 2048,
		ReasoningEffort: c.options.ReasoningEffort,
		Messages: []zeroruntime.Message{
			{
				Role: zeroruntime.MessageRoleSystem,
				Content: "Use the answer tool. Return a probability for each criterion. " +
					"For noul questions return false and true probabilities.",
			},
			{Role: zeroruntime.MessageRoleUser, Content: string(body)},
		},
		Tools: []zeroruntime.ToolDefinition{
			{Name: "answer", Description: "Return the decision probabilities.", Parameters: answerSchema},
		},
	}
	encoded, err := json.Marshal(completion)
	if err != nil {
		return zeroruntime.DecisionResult{}, err
	}
	if len(encoded) > c.options.MaxRequestBytes {
		return zeroruntime.DecisionResult{}, fmt.Errorf("provider decisions: request exceeds %d byte bound", c.options.MaxRequestBytes)
	}
	ctx, cancel := context.WithTimeout(ctx, c.options.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return zeroruntime.DecisionResult{}, err
	}
	events, err := c.provider.StreamCompletion(ctx, completion)
	// Match the typed-stage compatibility retry. Retry only a rejected forced tool.
	if err != nil && strings.HasSuffix(strings.TrimSuffix(strings.TrimSpace(err.Error()), "."), "provider request error: Provider returned error") {
		completion.ToolChoice = ""
		events, err = c.provider.StreamCompletion(ctx, completion)
	}
	if err != nil {
		return zeroruntime.DecisionResult{}, fmt.Errorf("provider decisions: %w", err)
	}
	result := zeroruntime.DecisionResult{ResolvedModel: c.options.Model, Provider: "provider"}
	fail := func(err error) (zeroruntime.DecisionResult, error) {
		return result, &zeroruntime.DecisionResponseError{Cause: err, Usage: &result}
	}
	var arguments strings.Builder
	calls := 0
	name := ""
	size := 0
	for {
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case event, ok := <-events:
			if !ok {
				goto complete
			}
			size += len(event.Content) + len(event.ArgumentsFragment)
			if size > c.options.MaxResponseBytes {
				return fail(fmt.Errorf("provider decisions: response exceeds %d byte bound", c.options.MaxResponseBytes))
			}
			switch event.Type {
			case zeroruntime.StreamEventToolCallStart:
				calls++
				name = event.ToolName
			case zeroruntime.StreamEventToolCallDelta:
				arguments.WriteString(event.ArgumentsFragment)
			case zeroruntime.StreamEventUsage:
				result.InputTokens = event.Usage.EffectiveInputTokens()
				result.OutputTokens = event.Usage.EffectiveOutputTokens()
				result.ReportedCostUSD = event.ReportedCostUSD
				result.RequestID = event.RequestID
			case zeroruntime.StreamEventError:
				return fail(fmt.Errorf("provider decisions: stream: %s", event.Error))
			}
		}
	}
complete:
	if calls != 1 || name != "answer" {
		return fail(fmt.Errorf("provider decisions: required one answer tool, got %d calls (%q)", calls, name))
	}
	var wire struct {
		Answers []struct {
			QuestionID    string             `json:"question_id"`
			Probabilities map[string]float64 `json:"probabilities"`
		} `json:"answers"`
	}
	if err := json.Unmarshal([]byte(arguments.String()), &wire); err != nil {
		return fail(fmt.Errorf("provider decisions: decode answer: %w", err))
	}
	for _, a := range wire.Answers {
		answer := zeroruntime.DecisionAnswer{QuestionID: a.QuestionID, Probabilities: a.Probabilities}
		for _, q := range req.Questions {
			if q.ID == a.QuestionID {
				answer.Kind = q.Kind
				top := -1.0
				for _, criterion := range q.Criteria {
					if probability := a.Probabilities[criterion.Label]; probability > top {
						top = probability
						answer.Choice = criterion.Label
					}
				}
				break
			}
		}
		if answer.Kind == zeroruntime.DecisionKindNoul {
			p, ok := a.Probabilities["true"]
			if !ok {
				return fail(fmt.Errorf("question %q: missing true probability", a.QuestionID))
			}
			// Validate both noul labels with the choice distribution contract.
			check := zeroruntime.DecisionRequest{
				Model: req.Model,
				State: req.State,
				Questions: []zeroruntime.DecisionQuestion{
					{
						ID:           a.QuestionID,
						Kind:         zeroruntime.DecisionKindChoice,
						Instructions: "noul distribution",
						Criteria:     []zeroruntime.DecisionCriterion{{Label: "false"}, {Label: "true"}},
					},
				},
			}
			distribution := zeroruntime.DecisionResult{
				ResolvedModel: result.ResolvedModel,
				Answers: []zeroruntime.DecisionAnswer{
					{
						QuestionID:    a.QuestionID,
						Kind:          zeroruntime.DecisionKindChoice,
						Choice:        answer.Choice,
						Probabilities: a.Probabilities,
					},
				},
			}
			if err := zeroruntime.ValidateDecisionResult(check, distribution); err != nil {
				return fail(err)
			}
			answer.TrueProbability = p
		}
		result.Answers = append(result.Answers, answer)
	}
	if err := zeroruntime.ValidateDecisionResult(req, result); err != nil {
		return fail(err)
	}
	return result, nil
}
