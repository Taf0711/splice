// Package httpdecisions implements the Splice HTTP decision contract v1.
package httpdecisions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/zeroruntime"
)

// Contract is the version identifier for the HTTP request.
const Contract = "splice.decision.v1"

// Options sets the endpoint, credentials, body limits, and timeout.
type Options struct {
	Endpoint, APIKey                  string
	MaxRequestBytes, MaxResponseBytes int
	Timeout                           time.Duration
	HTTPClient                        *http.Client
}

// Client evaluates decisions through the Splice HTTP contract.
type Client struct{ options Options }

// NewClient applies default limits and creates an HTTP decision client.
func NewClient(o Options) *Client {
	if o.MaxRequestBytes == 0 {
		o.MaxRequestBytes = 16384
	}
	if o.MaxResponseBytes == 0 {
		o.MaxResponseBytes = 65536
	}
	if o.Timeout == 0 {
		o.Timeout = 1500 * time.Millisecond
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{}
	}
	return &Client{options: o}
}

// Criterion describes one outcome in an HTTP question.
type Criterion struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Question is one typed question in the HTTP request.
type Question struct {
	ID           string                           `json:"id"`
	Kind         zeroruntime.DecisionQuestionKind `json:"kind"`
	Instructions string                           `json:"instructions"`
	Criteria     []Criterion                      `json:"criteria"`
}

// Request is the Splice HTTP decision contract v1 request.
type Request struct {
	Contract  string     `json:"contract"`
	Model     string     `json:"model"`
	SessionID string     `json:"session_id,omitempty"`
	State     string     `json:"state"`
	Questions []Question `json:"questions"`
}

// EncodeRequest converts a typed request to the HTTP wire format.
func EncodeRequest(req zeroruntime.DecisionRequest) ([]byte, error) {
	wire := Request{Contract: Contract, Model: req.Model, SessionID: req.SessionID, State: req.State}
	for _, q := range req.Questions {
		w := Question{ID: q.ID, Kind: q.Kind, Instructions: q.Instructions}
		for _, c := range q.Criteria {
			w.Criteria = append(w.Criteria, Criterion{c.Label, c.Description})
		}
		wire.Questions = append(wire.Questions, w)
	}
	return json.Marshal(wire)
}

// Answer contains the probabilities from the HTTP service.
type Answer struct {
	QuestionID      string                           `json:"question_id"`
	Kind            zeroruntime.DecisionQuestionKind `json:"kind"`
	Probabilities   map[string]float64               `json:"probabilities"`
	TrueProbability *float64                         `json:"true_probability"`
	Confidence      *float64                         `json:"confidence"`
}

// Response is the HTTP answer and usage envelope.
type Response struct {
	RequestID     string   `json:"request_id"`
	ResolvedModel string   `json:"resolved_model"`
	Provider      string   `json:"provider"`
	Answers       []Answer `json:"answers"`
	Usage         struct {
		InputTokens  int      `json:"input_tokens"`
		OutputTokens int      `json:"output_tokens"`
		CostUSD      *float64 `json:"cost_usd"`
	} `json:"usage"`
}

// DecodeResponse validates an HTTP answer and retains reported usage.
func DecodeResponse(req zeroruntime.DecisionRequest, raw []byte) (zeroruntime.DecisionResult, error) {
	var wire Response
	// Decode usage separately so a malformed answer cannot erase the charge.
	var accounting struct {
		RequestID     string `json:"request_id"`
		ResolvedModel string `json:"resolved_model"`
		Provider      string `json:"provider"`
		Usage         struct {
			InputTokens  int      `json:"input_tokens"`
			OutputTokens int      `json:"output_tokens"`
			CostUSD      *float64 `json:"cost_usd"`
		} `json:"usage"`
	}
	result := zeroruntime.DecisionResult{}
	if json.Unmarshal(raw, &accounting) == nil {
		result = zeroruntime.DecisionResult{
			RequestID:       accounting.RequestID,
			ResolvedModel:   accounting.ResolvedModel,
			Provider:        accounting.Provider,
			InputTokens:     accounting.Usage.InputTokens,
			OutputTokens:    accounting.Usage.OutputTokens,
			ReportedCostUSD: accounting.Usage.CostUSD,
		}
	}
	fail := func(err error) (zeroruntime.DecisionResult, error) {
		return result, &zeroruntime.DecisionResponseError{Cause: err, Usage: &result}
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return fail(fmt.Errorf("http decisions: decode response: %w", err))
	}
	for _, a := range wire.Answers {
		answer := zeroruntime.DecisionAnswer{QuestionID: a.QuestionID, Kind: a.Kind, Probabilities: a.Probabilities}
		// The HTTP contract supplies a distribution, without a selected label.
		// Choose the first maximum in criterion order before shared validation.
		if a.Kind == zeroruntime.DecisionKindChoice {
			for _, question := range req.Questions {
				if question.ID != a.QuestionID {
					continue
				}
				top := -1.0
				for _, criterion := range question.Criteria {
					if probability := a.Probabilities[criterion.Label]; probability > top {
						top = probability
						answer.Choice = criterion.Label
					}
				}
			}
		}
		if a.Confidence != nil {
			answer.Confidence = *a.Confidence
			answer.ConfidenceSet = true
		}
		if a.Kind == zeroruntime.DecisionKindNoul {
			answer.TrueProbability = math.NaN()
			if a.TrueProbability != nil {
				answer.TrueProbability = *a.TrueProbability
			}
		}
		result.Answers = append(result.Answers, answer)
	}
	if err := zeroruntime.ValidateDecisionResult(req, result); err != nil {
		return fail(err)
	}
	return result, nil
}

// Evaluate sends one bounded HTTP request and validates its response.
func (c *Client) Evaluate(ctx context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
	if c == nil {
		return zeroruntime.DecisionResult{}, fmt.Errorf("http decisions: evaluator is not wired")
	}
	if err := req.Validate(); err != nil {
		return zeroruntime.DecisionResult{}, err
	}
	o := c.options
	body, err := EncodeRequest(req)
	if err != nil {
		return zeroruntime.DecisionResult{}, err
	}
	if len(body) > o.MaxRequestBytes {
		return zeroruntime.DecisionResult{}, fmt.Errorf("http decisions: request exceeds %d byte bound", o.MaxRequestBytes)
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, o.Endpoint, bytes.NewReader(body))
	if err != nil {
		return zeroruntime.DecisionResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+o.APIKey)
	}
	response, err := o.HTTPClient.Do(request)
	if err != nil {
		return zeroruntime.DecisionResult{}, fmt.Errorf("http decisions: transport: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, int64(o.MaxResponseBytes)+1))
	if err != nil {
		return zeroruntime.DecisionResult{}, fmt.Errorf("http decisions: response: %w", err)
	}
	if len(raw) > o.MaxResponseBytes {
		return zeroruntime.DecisionResult{}, fmt.Errorf("http decisions: response exceeds %d byte bound", o.MaxResponseBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return zeroruntime.DecisionResult{}, fmt.Errorf("http decisions: status %d: %s", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	return DecodeResponse(req, raw)
}
