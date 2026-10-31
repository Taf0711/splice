package zeroruntime

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// The decision seam: a typed request/response contract for model-backed
// decision evaluators (the Jev integration). Decisions are a different
// transport from text completion: no stream, no proposal, no tool calls. The
// evaluator consumes supplied evidence and returns calibrated typed answers.
// It never generates a patch, a rationale, or a memory claim, and it never
// grants permissions: authority stays with the host.

// DecisionQuestionKind enumerates the supported question kinds. Choice is the
// initial integration; Noul (a stated yes/no proposition) follows for the
// trajectory experiment; Score stays deferred until a need exists.
type DecisionQuestionKind string

const (
	DecisionKindChoice DecisionQuestionKind = "choice"
	DecisionKindNoul   DecisionQuestionKind = "noul"
)

// DecisionCriterion is one labeled criterion of a question. For a choice
// question each criterion is one selectable alternative; for a noul question
// the two criteria describe the false and true outcomes.
type DecisionCriterion struct {
	Label       string
	Description string
}

// DecisionQuestion is one typed question in a decision request. Question IDs
// must be unique within a request; every answer references its question by
// ID, and the semantic criteria live on the question, never on the answer.
type DecisionQuestion struct {
	ID           string
	Kind         DecisionQuestionKind
	Instructions string
	Criteria     []DecisionCriterion
}

// DecisionRequest is one decision evaluation: the pinned model, the opaque
// serialized state to evaluate (produced from a validated host snapshot),
// and the typed questions. The state crosses the boundary exactly as the
// host serialized it; the adapter never interprets it.
type DecisionRequest struct {
	// Model is the pinned model ID (for example "typesafe/jev-1.13"). The
	// resolved model returned by the backend must carry this ID as a
	// prefix; anything else is version drift and fails loud.
	Model string
	// State is the opaque serialized evaluation state (compact JSON).
	State string
	// Questions are the typed questions for this request.
	Questions []DecisionQuestion
	// SessionID optionally groups related requests for observability.
	SessionID string
}

// Validate enforces the request contract before any transport is attempted.
func (r DecisionRequest) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("decision request: model is required")
	}
	if strings.TrimSpace(r.State) == "" {
		return fmt.Errorf("decision request: state is required")
	}
	if len(r.Questions) == 0 {
		return fmt.Errorf("decision request: at least one question is required")
	}
	seen := map[string]bool{}
	for _, q := range r.Questions {
		if strings.TrimSpace(q.ID) == "" {
			return fmt.Errorf("decision request: question id is required")
		}
		if seen[q.ID] {
			return fmt.Errorf("decision request: duplicate question id %q", q.ID)
		}
		seen[q.ID] = true
		if strings.TrimSpace(q.Instructions) == "" {
			return fmt.Errorf("decision request: question %q requires instructions", q.ID)
		}
		switch q.Kind {
		case DecisionKindChoice:
			if len(q.Criteria) < 2 {
				return fmt.Errorf("decision request: choice question %q requires at least two criteria", q.ID)
			}
		case DecisionKindNoul:
			if len(q.Criteria) != 2 {
				return fmt.Errorf("decision request: noul question %q requires exactly two criteria (false, true)", q.ID)
			}
			if q.Criteria[0].Label != "false" || q.Criteria[1].Label != "true" {
				return fmt.Errorf("decision request: noul question %q criteria must be labeled false then true", q.ID)
			}
		default:
			return fmt.Errorf("decision request: question %q has unsupported kind %q", q.ID, q.Kind)
		}
		labels := map[string]bool{}
		for _, c := range q.Criteria {
			if strings.TrimSpace(c.Label) == "" {
				return fmt.Errorf("decision request: question %q has a criterion without a label", q.ID)
			}
			if labels[c.Label] {
				return fmt.Errorf("decision request: question %q repeats criterion label %q", q.ID, c.Label)
			}
			labels[c.Label] = true
		}
	}
	return nil
}

// DecisionAnswer is one validated typed answer. For a choice answer, Choice
// is the selected label and Probabilities carries every criterion label with
// its probability (a label the backend omitted is recorded as zero, never
// dropped). For a noul answer, TrueProbability is the probability of the
// true criterion. Confidence is optional: ConfidenceSet distinguishes an
// absent confidence from a reported zero, so certainty is never invented.
type DecisionAnswer struct {
	QuestionID      string
	Kind            DecisionQuestionKind
	Choice          string
	TrueProbability float64
	Probabilities   map[string]float64
	Confidence      float64
	ConfidenceSet   bool
}

// DecisionResult is one decision evaluation result: typed answers plus the
// accounting identity a later reconciliation needs.
type DecisionResult struct {
	// RequestID is the backend's request/correlation identity, when reported.
	RequestID string
	// ResolvedModel is the model version the backend actually evaluated. It
	// may carry a dated suffix beyond the pinned ID; anything else is drift.
	ResolvedModel string
	// Provider names the backend that evaluated the request.
	Provider string
	// Answers are the validated typed answers, one per requested question.
	Answers []DecisionAnswer
	// InputTokens and OutputTokens are the reported usage. Free output is
	// still output: the counts are recorded even when the cost is zero.
	InputTokens  int
	OutputTokens int
	// ReportedCostUSD is the backend-reported exact charge, when present.
	// nil means no charge was reported, never that the request was free.
	ReportedCostUSD *float64
}

// AnswerByID returns the validated answer for one question ID.
func (r DecisionResult) AnswerByID(id string) (DecisionAnswer, bool) {
	for _, a := range r.Answers {
		if a.QuestionID == id {
			return a, true
		}
	}
	return DecisionAnswer{}, false
}

// DecisionUsageError is an evaluation failure that still carries valid
// accounting information extracted from the response: a malformed answer
// still bills, and the caller's ledger must see it.
type DecisionUsageError interface {
	error
	DecisionUsage() *DecisionResult
}

// DecisionEvaluator is the provider-seam capability for model-backed
// decisions. Implementations own the transport: bounded bodies, no hidden
// retries, context cancellation, and typed errors that preserve any valid
// charge information. The interface never grants task authority: the host
// decides what to do with an answer through its own economic and safety
// gates.
type DecisionEvaluator interface {
	Evaluate(ctx context.Context, req DecisionRequest) (DecisionResult, error)
}

// ValidateDecisionResult checks the answer contract and fills omitted labels with zero.
// Confidence, kind, and selected-label errors keep the original adapter order.
func ValidateDecisionResult(req DecisionRequest, result DecisionResult) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if result.ResolvedModel != req.Model && !strings.HasPrefix(result.ResolvedModel, req.Model+"-") {
		return fmt.Errorf("resolved model %q does not match the pinned model %q", result.ResolvedModel, req.Model)
	}
	valid := func(p float64) bool { return !math.IsNaN(p) && !math.IsInf(p, 0) && p >= 0 && p <= 1 }
	if result.InputTokens < 0 || result.OutputTokens < 0 {
		return fmt.Errorf("decision usage: negative token count")
	}
	if result.ReportedCostUSD != nil && (math.IsNaN(*result.ReportedCostUSD) || math.IsInf(*result.ReportedCostUSD, 0) || *result.ReportedCostUSD < 0) {
		return fmt.Errorf("decision usage: invalid cost")
	}
	questions := map[string]DecisionQuestion{}
	for _, q := range req.Questions {
		questions[q.ID] = q
	}
	seen := map[string]bool{}
	for i := range result.Answers {
		a := &result.Answers[i]
		q, ok := questions[a.QuestionID]
		if !ok {
			return fmt.Errorf("answer for unknown question %q", a.QuestionID)
		}
		if seen[a.QuestionID] {
			return fmt.Errorf("duplicate answer for question %q", a.QuestionID)
		}
		seen[a.QuestionID] = true
		if a.ConfidenceSet && !valid(a.Confidence) {
			return fmt.Errorf("question %q: confidence outside [0,1]", q.ID)
		}
		if a.Kind != q.Kind {
			return fmt.Errorf("question %q: answer type %q does not match question kind %q", q.ID, a.Kind, q.Kind)
		}
		if q.Kind == DecisionKindNoul {
			if !valid(a.TrueProbability) {
				return fmt.Errorf("question %q: noul probability outside [0,1]", q.ID)
			}
			continue
		}
		labels := map[string]bool{}
		for _, c := range q.Criteria {
			labels[c.Label] = true
		}
		if a.Choice == "" {
			return fmt.Errorf("question %q: choice answer without a selected label", q.ID)
		}
		if !labels[a.Choice] {
			return fmt.Errorf("question %q: selected label %q is not one of the criteria", q.ID, a.Choice)
		}
		for label := range a.Probabilities {
			if !labels[label] {
				return fmt.Errorf("question %q: unknown probability label %q", q.ID, label)
			}
		}
		if a.Probabilities == nil {
			a.Probabilities = map[string]float64{}
		}
		sum := 0.0
		for _, c := range q.Criteria {
			p := a.Probabilities[c.Label]
			if !valid(p) {
				return fmt.Errorf("question %q: probability for %q outside [0,1]", q.ID, c.Label)
			}
			a.Probabilities[c.Label] = p
			sum += p
		}
		if math.Abs(sum-1) > 0.02 {
			return fmt.Errorf("question %q: probability sum %v deviates from 1 by more than 0.02", q.ID, sum)
		}
	}
	for _, q := range req.Questions {
		if !seen[q.ID] {
			return fmt.Errorf("missing answer for question %q", q.ID)
		}
	}
	return nil
}

// DecisionResponseError preserves reported usage when an answer fails validation.
type DecisionResponseError struct {
	Cause error
	Usage *DecisionResult
}

// Error returns the response failure.
func (e *DecisionResponseError) Error() string {
	if e.Cause == nil {
		return "invalid decision response"
	}
	return e.Cause.Error()
}

// Unwrap returns the response failure for error checks.
func (e *DecisionResponseError) Unwrap() error { return e.Cause }

// DecisionUsage returns the reported usage if its values are valid.
func (e *DecisionResponseError) DecisionUsage() *DecisionResult {
	if e.Usage == nil || e.Usage.InputTokens < 0 || e.Usage.OutputTokens < 0 {
		return nil
	}
	if cost := e.Usage.ReportedCostUSD; cost != nil && (math.IsNaN(*cost) || math.IsInf(*cost, 0) || *cost < 0) {
		return nil
	}
	return e.Usage
}
