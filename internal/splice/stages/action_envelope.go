package stages

// Work package D1 (warm-cost handoff Section 8): the discriminated action
// envelope in the existing forced submission tool.
//
// One typed tool per stage stays. Its single call now carries exactly one
// of two actions:
//
//   - request_context: a bounded ContextRequest ONLY. The host validates
//     and fulfills it, then re-invokes the stage with new evidence.
//   - submit_changes: the C proposal (files array), the terminal action.
//
// Validation rejects both or neither: a payload with neither field is
// malformed; a payload with BOTH is rejected without side effects (a
// context request can never also apply edits). Memory disposition claims
// are parsed independently of substantive action validity, so malformed
// bookkeeping neither discards valid edits nor triggers a retry.
//
// Limits (Section 8 D1): the host validates query count, byte limits,
// paths, and progress on the request_context action.

import (
	"encoding/json"
	"fmt"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// Action envelope field names. The discriminated union rides the same
// forced tool: exactly one of "request_context" or "submit_changes".
const (
	actionFieldContext = "request_context"
	actionFieldChanges = "submit_changes"
)

// maxActionContextQueries bounds the queries one request_context action
// may carry (Section 8 D2 starting value: 4 per expansion request).
const maxActionContextQueries = 4

// maxActionContextBytes bounds the summed MaxChars of one request_context
// action (4 x 12000 for range reads of implementation files).
const maxActionContextBytes = 48000

// StageAction is the decoded, discriminated outcome of one stage tool
// call. Exactly one of Request/Proposal is non-nil.
type StageAction struct {
	// Request is non-nil when the model chose request_context.
	Request *schemas.ContextRequest
	// Proposal is non-nil when the model chose submit_changes: the raw
	// args string for the stage's existing typed parser (which owns
	// compact/1 normalization and legacy compatibility).
	ProposalArgs string
	// MemoryClaimsRideAlong carries the raw args regardless of action so
	// disposition parsing stays independent of substantive validity.
	RawArgs string
}

// DecodeStageAction discriminates the envelope from one tool call's args.
// Rules:
//   - neither field present: malformed (typed error, no side effects);
//   - both fields present: rejected (context and edits are mutually
//     exclusive in one action);
//   - request_context: validated as a bounded context request
//     (Validate + AggregateValidate + the action's own tighter bounds);
//   - submit_changes: args passed through for the stage's parser.
func DecodeStageAction(toolName, args string) (StageAction, error) {
	var probe struct {
		RequestContext *json.RawMessage `json:"request_context"`
		SubmitChanges  *json.RawMessage `json:"submit_changes"`
	}
	if err := json.Unmarshal([]byte(args), &probe); err != nil {
		return StageAction{}, fmt.Errorf("parse %s action envelope: %w", toolName, err)
	}
	hasContext := probe.RequestContext != nil
	hasChanges := probe.SubmitChanges != nil
	switch {
	case !hasContext && !hasChanges:
		return StageAction{}, fmt.Errorf("%s action: exactly one of %q or %q is required (neither present)", toolName, actionFieldContext, actionFieldChanges)
	case hasContext && hasChanges:
		return StageAction{}, fmt.Errorf("%s action: a context request and a change submission are mutually exclusive; both were present and neither was applied", toolName)
	}
	if hasChanges {
		return StageAction{ProposalArgs: args, RawArgs: args}, nil
	}
	// request_context: decode and bound-check before anything executes.
	var request schemas.ContextRequest
	if err := json.Unmarshal(*probe.RequestContext, &request); err != nil {
		return StageAction{}, fmt.Errorf("parse %s request_context: %w", toolName, err)
	}
	if err := ValidateActionContextRequest(request); err != nil {
		return StageAction{}, err
	}
	return StageAction{Request: &request, RawArgs: args}, nil
}

// ValidateActionContextRequest applies the ACTION-scoped bounds (tighter
// than the generic request bounds): query count, aggregate bytes, and the
// allowed query types. Shell-shaped or otherwise unbounded queries are
// rejected here; the model can only ask for file/range reads, qualified
// symbol reads, and bounded searches.
func ValidateActionContextRequest(request schemas.ContextRequest) error {
	if len(request.Queries) == 0 {
		return fmt.Errorf("request_context carries no queries")
	}
	if len(request.Queries) > maxActionContextQueries {
		return fmt.Errorf("request_context carries %d queries, more than the %d-query bound", len(request.Queries), maxActionContextQueries)
	}
	total := 0
	for i, q := range request.Queries {
		if err := q.Validate(); err != nil {
			return fmt.Errorf("request_context queries[%d]: %w", i, err)
		}
		switch q.QueryType {
		case schemas.ContextReadFile, schemas.ContextOutline, schemas.ContextSearch, schemas.ContextFindSymbol, schemas.ContextGetSymbol:
			// supported bounded shapes
		case schemas.ContextListFiles:
			return fmt.Errorf("request_context queries[%d]: repository-wide listing is not an expansion query; request specific paths", i)
		default:
			return fmt.Errorf("request_context queries[%d]: unsupported query type %q", i, q.QueryType)
		}
		total += q.MaxChars
	}
	if total > maxActionContextBytes {
		return fmt.Errorf("request_context aggregates %d max-chars, more than the %d-byte bound", total, maxActionContextBytes)
	}
	if err := request.AggregateValidate(); err != nil {
		return err
	}
	return nil
}

// TryDecodeStageAction is the non-allocating probe used by the typed
// retry loop: it returns the action when the payload carries the envelope
// and the stage's legacy error otherwise. Stages that predate the
// envelope (a model that answered with the bare proposal) keep working
// because the envelope is OPTIONAL at decode time for backward
// compatibility: args without either field but WITH a files array decode
// as submit_changes.
func TryDecodeStageAction(toolName string, collected *zeroruntime.CollectedStream) (StageAction, error) {
	tc := findToolCall(collected, toolName)
	if tc == nil {
		return StageAction{}, fmt.Errorf("model did not call %s", toolName)
	}
	stripped, err := stripDispositionClaims(tc.Arguments)
	if err != nil {
		return StageAction{}, fmt.Errorf("parse %s args: %w", toolName, err)
	}
	action, err := DecodeStageAction(toolName, stripped)
	if err != nil {
		// Backward compatibility: a bare C-protocol payload (files array
		// present as a JSON field, even empty, and no envelope fields) is
		// a submit_changes action. The legacy full/1 and compact/1 forms
		// both carry "files".
		var bare struct {
			Files json.RawMessage `json:"files"`
		}
		if jsonErr := json.Unmarshal([]byte(stripped), &bare); jsonErr == nil && bare.Files != nil {
			return StageAction{ProposalArgs: stripped, RawArgs: stripped}, nil
		}
		return StageAction{}, err
	}
	return action, nil
}
