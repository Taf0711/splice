// Package openrouterdecisions implements the zeroruntime.DecisionEvaluator
// seam over OpenRouter's alpha Decisions endpoint. It owns the transport
// only: bounded bodies, zero automatic retries, context cancellation, typed
// error classification, and response validation. Pipeline permissions,
// budget authority, and task success stay with the host.
package openrouterdecisions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/zeroruntime"
)

// DefaultEndpoint is OpenRouter's alpha Decisions endpoint. It is a separate
// contract from chat completions: never append /alpha/decisions to a chat
// base URL that already carries /api/v1.
const DefaultEndpoint = "https://openrouter.ai/api/alpha/decisions"

// Transport bounds. They cap a malformed or hostile response, not the
// provider's token limits: a conservative billable-token bound is computed
// separately by the caller's budget gate.
const (
	defaultMaxRequestBytes  = 64 * 1024
	defaultMaxResponseBytes = 256 * 1024
	defaultTimeout          = 15 * time.Second
)

// Client evaluates decisions over OpenRouter's alpha Decisions endpoint.
// A nil-pointer method set is safe: Evaluate reports a typed error instead
// of panicking, so an unwired capability degrades instead of crashing.
type Client struct {
	apiKey           string
	endpoint         string
	httpClient       *http.Client
	maxRequestBytes  int
	maxResponseBytes int
}

// Option configures a Client.
type Option func(*Client)

// WithEndpoint overrides the endpoint (tests; isolated deployments).
func WithEndpoint(endpoint string) Option {
	return func(c *Client) { c.endpoint = endpoint }
}

// WithHTTPClient overrides the HTTP client (tests; proxies).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// WithMaxRequestBytes overrides the request body bound.
func WithMaxRequestBytes(n int) Option {
	return func(c *Client) { c.maxRequestBytes = n }
}

// WithMaxResponseBytes overrides the response body bound.
func WithMaxResponseBytes(n int) Option {
	return func(c *Client) { c.maxResponseBytes = n }
}

// NewClient builds a decision evaluator over the alpha Decisions endpoint.
// The API key is resolved by the caller (env, keyring, or profile); an empty
// key produces an evaluator whose Evaluate fails loud with a named error,
// never a silent unauthenticated attempt.
func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:           apiKey,
		endpoint:         DefaultEndpoint,
		httpClient:       &http.Client{Timeout: defaultTimeout},
		maxRequestBytes:  defaultMaxRequestBytes,
		maxResponseBytes: defaultMaxResponseBytes,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// typed errors. Callers classify degradation without string matching.
var (
	errNoAPIKey   = errors.New("openrouter decisions: no API key configured")
	errNoEndpoint = errors.New("openrouter decisions: no endpoint configured")
)

// StatusCodeError is a non-2xx response with its classified status. The
// classification is informational: every non-2xx degrades the same way
// (no retry, named failure), but the report names the cause.
type StatusCodeError struct {
	Status int
	Body   string
}

func (e *StatusCodeError) Error() string {
	return fmt.Sprintf("openrouter decisions: status %d: %s", e.Status, e.Body)
}

// ResponseError wraps a 2xx response whose body did not satisfy the
// contract. Usage carries any valid accounting information extracted before
// the failure: a malformed answer still bills, and the ledger must see it.
type ResponseError struct {
	Reason string
	// Usage is the accounting information extracted from the response, when
	// parseable. nil fields mean "not extracted", never zero.
	Usage *zeroruntime.DecisionResult
}

func (e *ResponseError) Error() string { return "openrouter decisions: " + e.Reason }

// DecisionUsage implements zeroruntime.DecisionUsageError so callers at the
// seam can recover billed usage from a failed evaluation without importing
// this package.
func (e *ResponseError) DecisionUsage() *zeroruntime.DecisionResult {
	return (&zeroruntime.DecisionResponseError{Usage: e.Usage}).DecisionUsage()
}

// wire types mirror the documented Decisions contract exactly. Field names
// are the wire names; the seam types are translated at this boundary only.
type wireQuestion struct {
	Criteria     json.RawMessage `json:"criteria"`
	Instructions string          `json:"instructions"`
	Type         string          `json:"type"`
}

type wireRequest struct {
	Model     string                  `json:"model"`
	Questions map[string]wireQuestion `json:"questions"`
	State     json.RawMessage         `json:"state"`
	SessionID string                  `json:"session_id,omitempty"`
}

type wireAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        *string            `json:"choice,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

type wireUsage struct {
	Cost         *float64 `json:"cost,omitempty"`
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens"`
}

type wireResponse struct {
	Answers  map[string]wireAnswer `json:"answers"`
	Model    string                `json:"model"`
	Provider string                `json:"provider"`
	ID       string                `json:"id"`
	Usage    wireUsage             `json:"usage"`
}

// Evaluate implements zeroruntime.DecisionEvaluator. One HTTP attempt, no
// automatic retries: a failed decision degrades to the host's baseline
// policy, and a retry loop here would multiply spend outside the budget
// gate's reservation.
func (c *Client) Evaluate(ctx context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
	if c == nil {
		return zeroruntime.DecisionResult{}, errors.New("openrouter decisions: evaluator is not wired")
	}
	if err := req.Validate(); err != nil {
		return zeroruntime.DecisionResult{}, err
	}
	if strings.TrimSpace(c.apiKey) == "" {
		return zeroruntime.DecisionResult{}, errNoAPIKey
	}
	if c.endpoint == "" {
		return zeroruntime.DecisionResult{}, errNoEndpoint
	}

	body, err := encodeRequest(req)
	if err != nil {
		return zeroruntime.DecisionResult{}, err
	}
	if len(body) > c.maxRequestBytes {
		return zeroruntime.DecisionResult{}, fmt.Errorf("openrouter decisions: request body %d bytes exceeds the %d byte bound", len(body), c.maxRequestBytes)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return zeroruntime.DecisionResult{}, fmt.Errorf("openrouter decisions: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// A caller-driven cancellation surfaces verbatim; other transport
		// errors are named without humanizing into an upstream outage.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return zeroruntime.DecisionResult{}, fmt.Errorf("openrouter decisions: %w", ctxErr)
		}
		return zeroruntime.DecisionResult{}, fmt.Errorf("openrouter decisions: transport: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, int64(c.maxResponseBytes)+1))
	if err != nil {
		return zeroruntime.DecisionResult{}, fmt.Errorf("openrouter decisions: read response: %w", err)
	}
	if len(raw) > c.maxResponseBytes {
		return zeroruntime.DecisionResult{}, fmt.Errorf("openrouter decisions: response exceeds the %d byte bound", c.maxResponseBytes)
	}
	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		return zeroruntime.DecisionResult{}, &StatusCodeError{Status: httpResp.StatusCode, Body: strings.TrimSpace(string(raw))}
	}

	var resp wireResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		var accounting struct {
			Model    string    `json:"model"`
			Provider string    `json:"provider"`
			ID       string    `json:"id"`
			Usage    wireUsage `json:"usage"`
		}
		var usage *zeroruntime.DecisionResult
		if json.Unmarshal(raw, &accounting) == nil {
			usage = &zeroruntime.DecisionResult{ResolvedModel: accounting.Model, Provider: accounting.Provider, RequestID: accounting.ID, InputTokens: accounting.Usage.InputTokens, OutputTokens: accounting.Usage.OutputTokens, ReportedCostUSD: accounting.Usage.Cost}
		}
		return zeroruntime.DecisionResult{}, &ResponseError{Reason: fmt.Sprintf("decode response: %v", err), Usage: usage}
	}
	result, perr := translate(req, resp)
	if perr != nil {
		// Preserve the accounting identity even when the answers are
		// unusable: a malformed answer still bills.
		return result, &ResponseError{Reason: perr.Error(), Usage: &result}
	}
	return result, nil
}

// encodeRequest builds the wire body from the typed request. The state is
// embedded as raw JSON exactly as the host serialized it.
func encodeRequest(req zeroruntime.DecisionRequest) ([]byte, error) {
	if !json.Valid([]byte(req.State)) {
		return nil, fmt.Errorf("openrouter decisions: state is not valid JSON")
	}
	questions := make(map[string]wireQuestion, len(req.Questions))
	for _, q := range req.Questions {
		// Both supported kinds use the record form: criteria as a
		// label-to-description object. The array form is the score
		// contract, which stays deferred; the live smoke proved the
		// endpoint rejects an array for choice questions ("expected
		// record, received array").
		obj := map[string]string{}
		for _, cr := range q.Criteria {
			obj[cr.Label] = cr.Description
		}
		raw, err := json.Marshal(obj)
		if err != nil {
			return nil, fmt.Errorf("openrouter decisions: encode criteria for %q: %w", q.ID, err)
		}
		questions[q.ID] = wireQuestion{Criteria: raw, Instructions: q.Instructions, Type: string(q.Kind)}
	}
	wire := wireRequest{
		Model:     req.Model,
		Questions: questions,
		State:     json.RawMessage(req.State),
		SessionID: req.SessionID,
	}
	out, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("openrouter decisions: encode request: %w", err)
	}
	return out, nil
}

// translate validates the decoded response into the typed result.
func translate(req zeroruntime.DecisionRequest, resp wireResponse) (zeroruntime.DecisionResult, error) {
	result := zeroruntime.DecisionResult{
		RequestID:       resp.ID,
		ResolvedModel:   resp.Model,
		Provider:        resp.Provider,
		InputTokens:     resp.Usage.InputTokens,
		OutputTokens:    resp.Usage.OutputTokens,
		ReportedCostUSD: resp.Usage.Cost,
	}
	for id, wa := range resp.Answers {
		a := zeroruntime.DecisionAnswer{QuestionID: id, Kind: zeroruntime.DecisionQuestionKind(wa.Type), Probabilities: wa.Probabilities}
		if wa.Confidence != nil {
			a.Confidence = *wa.Confidence
			a.ConfidenceSet = true
		}
		if wa.Choice != nil {
			a.Choice = *wa.Choice
		}
		if a.Kind == zeroruntime.DecisionKindNoul {
			a.TrueProbability = math.NaN()
			if wa.Noul != nil {
				a.TrueProbability = *wa.Noul
			}
		}
		result.Answers = append(result.Answers, a)
	}
	return result, zeroruntime.ValidateDecisionResult(req, result)
}
