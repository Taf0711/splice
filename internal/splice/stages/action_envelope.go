package stages

// Work package D1 (warm-cost handoff Section 8): the discriminated action
// envelope in the existing forced submission tool.
//
// One typed tool per stage stays. Its single call carries exactly one of
// two actions:
//
//   - request_context: a bounded ContextRequest ONLY. The host validates
//     and fulfills it, then re-invokes the stage with new evidence.
//   - submit_changes: the C proposal (files array), the terminal action.
//
// The DECLARED form names the choice in a required `action` field with the
// enum {request_context, submit_changes}. The tool schema
// (submitCodeToolDefinition) and the stage prompt advertise that field. The
// decoder also accepts the older envelope form, which carries exactly one
// of the two payload fields and no `action`, and the flat legacy payload
// that carries a top-level files array.
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

// actionProbe is the raw shape of one stage tool call before dispatch.
// Files is read only to detect a flat submit that is mixed with a
// request_context action.
type actionProbe struct {
	Action         *string          `json:"action"`
	RequestContext *json.RawMessage `json:"request_context"`
	SubmitChanges  *json.RawMessage `json:"submit_changes"`
	Files          *json.RawMessage `json:"files"`
}

// DecodeStageAction discriminates the envelope from one tool call's args.
// Two contract forms are accepted:
//
//   - the DECLARED form: a required `action` field naming exactly one of
//     "request_context" or "submit_changes". The tool schema and the
//     stage prompt advertise this form.
//   - the LEGACY envelope form: no `action` field, exactly one of the
//     `request_context` or `submit_changes` fields present.
//
// Rules, for both forms:
//   - neither action present: malformed (typed error, no side effects);
//   - both actions present: rejected (context and edits are mutually
//     exclusive in one action);
//   - request_context: validated as a bounded context request
//     (Validate + AggregateValidate + the action's own tighter bounds);
//   - submit_changes: the inner object is UNWRAPPED and passed to the
//     stage parser, so the advertised envelope and the flat payload
//     both reach the same parser.
func DecodeStageAction(toolName, args string) (StageAction, error) {
	var probe actionProbe
	if err := json.Unmarshal([]byte(args), &probe); err != nil {
		return StageAction{}, fmt.Errorf("parse %s action envelope: %w", toolName, err)
	}
	if probe.Action != nil {
		return decodeDeclaredAction(toolName, args, *probe.Action, &probe)
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
		inner, err := unwrapSubmitChanges(toolName, *probe.SubmitChanges)
		if err != nil {
			return StageAction{}, err
		}
		return StageAction{ProposalArgs: inner, RawArgs: args}, nil
	}
	return decodeContextRequestAction(toolName, args, *probe.RequestContext)
}

// decodeDeclaredAction dispatches the advertised `action` discriminator.
// The declared action decides the payload; the legacy envelope fields are
// still accepted inside it so a model that follows either description
// lands on the same action.
func decodeDeclaredAction(toolName, args, action string, probe *actionProbe) (StageAction, error) {
	switch action {
	case actionFieldContext:
		if probe.SubmitChanges != nil || probe.Files != nil {
			return StageAction{}, fmt.Errorf("%s action %q: a context request and a change submission are mutually exclusive; both were present and neither was applied", toolName, action)
		}
		if probe.RequestContext == nil {
			return StageAction{}, fmt.Errorf("%s action %q requires the %q field", toolName, action, actionFieldContext)
		}
		return decodeContextRequestAction(toolName, args, *probe.RequestContext)
	case actionFieldChanges:
		if probe.RequestContext != nil {
			return StageAction{}, fmt.Errorf("%s action %q: a context request and a change submission are mutually exclusive; both were present and neither was applied", toolName, action)
		}
		if probe.SubmitChanges != nil {
			inner, err := unwrapSubmitChanges(toolName, *probe.SubmitChanges)
			if err != nil {
				return StageAction{}, err
			}
			return StageAction{ProposalArgs: inner, RawArgs: args}, nil
		}
		if probe.Files == nil {
			return StageAction{}, fmt.Errorf("%s action %q requires the %q field or a flat files array", toolName, action, actionFieldChanges)
		}
		// The schema requires only the discriminator, so the decoder keeps
		// the guarantee the old required list carried: a declared submit
		// must carry at least one file. CodeWriterOutput.Validate does not
		// check the list length, because a report-only stage may legally
		// return none.
		var files []json.RawMessage
		if err := json.Unmarshal(*probe.Files, &files); err != nil {
			return StageAction{}, fmt.Errorf("%s action %q: files must be an array: %w", toolName, action, err)
		}
		if len(files) == 0 {
			return StageAction{}, fmt.Errorf("%s action %q: files is empty; a submit must carry at least one file", toolName, action)
		}
		return StageAction{ProposalArgs: args, RawArgs: args}, nil
	default:
		return StageAction{}, fmt.Errorf("%s action %q is not one of %q or %q", toolName, action, actionFieldContext, actionFieldChanges)
	}
}

// decodeContextRequestAction decodes and bound-checks one request_context
// payload. It runs before anything executes.
func decodeContextRequestAction(toolName, args string, raw json.RawMessage) (StageAction, error) {
	var request schemas.ContextRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return StageAction{}, fmt.Errorf("parse %s request_context: %w", toolName, err)
	}
	if err := ValidateActionContextRequest(request); err != nil {
		return StageAction{}, err
	}
	return StageAction{Request: &request, RawArgs: args}, nil
}

// unwrapSubmitChanges returns the inner submit object as JSON so the
// advertised envelope and the flat payload reach the same stage parser.
// A submit envelope without a files array fails loud instead of decoding
// as an empty proposal.
func unwrapSubmitChanges(toolName string, raw json.RawMessage) (string, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", fmt.Errorf("%s action %q must be an object: %w", toolName, actionFieldChanges, err)
	}
	if _, ok := obj["files"]; !ok {
		return "", fmt.Errorf("%s action %q carries no files array", toolName, actionFieldChanges)
	}
	inner, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("%s action %q: re-encode inner object: %w", toolName, actionFieldChanges, err)
	}
	return string(inner), nil
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
		// Backward compatibility: a bare C-protocol payload with NO
		// declared action field (a flat files array, no envelope) is
		// a submit_changes action. The legacy full/1 and compact/1 forms
		// both carry "files". When an action IS declared, the error
		// must propagate so a declarative conflict (for example,
		// action=submit_changes with a request_context payload) fails
		// loud instead of being silently downgraded.
		var hasAction struct {
			Action *string `json:"action"`
		}
		if actionErr := json.Unmarshal([]byte(stripped), &hasAction); actionErr == nil && hasAction.Action != nil {
			return StageAction{}, err
		}
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

// contextRequestToolName is the named context-request tool. The action
// contract is expressed as two independently named tool schemas normalized
// into the same StageAction: a submission tool that carries no
// request_context property, and this tool. A model cannot attach a context
// payload to a submission through a field the submission schema does not
// declare, which is the failure mode the single-tool discriminator shape
// produced (six of eight recorded calls mixed the payloads and were
// rejected as format retries).
const contextRequestToolName = "request_codebase_context"

// contextRequestToolDefinition is the model-facing context-request tool:
// one bounded reason and one to four bounded queries, nothing else. The
// payload is the ContextRequest itself, not a nested envelope.
func contextRequestToolDefinition() zeroruntime.ToolDefinition {
	full := actionContextRequestSchema()
	return zeroruntime.ToolDefinition{
		Name:        contextRequestToolName,
		Description: "Ask the host for bounded source your context views did not deliver. The host fulfills the request and calls you again with the new evidence. reason is required and must name what you need and why.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reason":             full["properties"].(map[string]any)["reason"],
				"queries":            full["properties"].(map[string]any)["queries"],
				"memory_disposition": memoryDispositionSchema,
			},
			"required": []string{"reason", "queries"},
		},
	}
}

// TryDecodeStageActionFromTools decodes the one typed action from a stream
// that offered both the submission tool and the context-request tool.
// Exactly one tool call is allowed: both present is a conflict that fails
// loud and is retried as a format error, never silently downgraded. The
// submission branch keeps the legacy flat and envelope forms so a model
// that follows either description lands on the same action.
func TryDecodeStageActionFromTools(collected *zeroruntime.CollectedStream, submitToolName, contextToolName string) (StageAction, error) {
	submitCall := findToolCall(collected, submitToolName)
	contextCall := findToolCall(collected, contextToolName)
	switch {
	case submitCall != nil && contextCall != nil:
		return StageAction{}, fmt.Errorf("%s action: exactly one action is allowed; both %q and %q were called and neither was applied", submitToolName, submitToolName, contextToolName)
	case contextCall != nil:
		stripped, err := stripDispositionClaims(contextCall.Arguments)
		if err != nil {
			return StageAction{}, fmt.Errorf("parse %s args: %w", contextToolName, err)
		}
		var request schemas.ContextRequest
		if err := json.Unmarshal([]byte(stripped), &request); err != nil {
			return StageAction{}, fmt.Errorf("parse %s request_context: %w", contextToolName, err)
		}
		if err := ValidateActionContextRequest(request); err != nil {
			return StageAction{}, err
		}
		return StageAction{Request: &request, RawArgs: stripped}, nil
	case submitCall != nil:
		stripped, err := stripDispositionClaims(submitCall.Arguments)
		if err != nil {
			return StageAction{}, fmt.Errorf("parse %s args: %w", submitToolName, err)
		}
		return decodeSubmissionCall(submitToolName, stripped)
	default:
		return StageAction{}, fmt.Errorf("model did not call %s or %s", submitToolName, contextToolName)
	}
}

// decodeSubmissionCall validates one submission tool call's arguments. The
// two-tool schema declares files, language, intent, and confidence as
// required, so the decoder keeps the guarantee the schema states: a
// submission carries at least one file. A request_context key on a
// submission is a conflict and fails loud, even though the schema no
// longer advertises it.
func decodeSubmissionCall(toolName, stripped string) (StageAction, error) {
	var probe struct {
		Files          json.RawMessage `json:"files"`
		RequestContext json.RawMessage `json:"request_context"`
	}
	if err := json.Unmarshal([]byte(stripped), &probe); err != nil {
		return StageAction{}, fmt.Errorf("parse %s args: %w", toolName, err)
	}
	if probe.RequestContext != nil {
		return StageAction{}, fmt.Errorf("%s action: a context request and a change submission are mutually exclusive; both were present and neither was applied", toolName)
	}
	if probe.Files == nil {
		return StageAction{}, fmt.Errorf("%s requires the %q field", toolName, "files")
	}
	var files []json.RawMessage
	if err := json.Unmarshal(probe.Files, &files); err != nil {
		return StageAction{}, fmt.Errorf("%s: files must be an array: %w", toolName, err)
	}
	if len(files) == 0 {
		return StageAction{}, fmt.Errorf("%s: files is empty; a submit must carry at least one file", toolName)
	}
	return StageAction{ProposalArgs: stripped, RawArgs: stripped}, nil
}
