package stages

// D1 regression tests: the discriminated action envelope.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func dEnvelope(t *testing.T, payload map[string]any) string {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func dStream(args string) *zeroruntime.CollectedStream {
	return &zeroruntime.CollectedStream{ToolCalls: []zeroruntime.ToolCall{{Name: "submit_code", Arguments: args}}}
}

// TestDecodeRequestContextAction pins the context action: a bounded
// ContextRequest decodes; the proposal stays nil.
func TestDecodeRequestContextAction(t *testing.T) {
	args := dEnvelope(t, map[string]any{
		"request_context": map[string]any{
			"reason": "need the storage dependency",
			"queries": []map[string]any{
				{"query_type": "read_file", "path": "storage.go", "max_results": 5, "max_chars": 4000},
			},
		},
	})
	action, err := DecodeStageAction("submit_code", args)
	if err != nil {
		t.Fatal(err)
	}
	if action.Request == nil || action.Request.Reason != "need the storage dependency" {
		t.Fatalf("request not decoded: %+v", action)
	}
	if len(action.Request.Queries) != 1 || *action.Request.Queries[0].Path != "storage.go" {
		t.Fatalf("queries wrong: %+v", action.Request.Queries)
	}
}

// TestDecodeSubmitChangesAction pins the terminal action: the files array
// passes through for the stage parser.
func TestDecodeSubmitChangesAction(t *testing.T) {
	args := dEnvelope(t, map[string]any{
		"submit_changes": map[string]any{
			"files":    []map[string]any{{"path": "a.go", "change_type": "create", "content": "package a\n"}},
			"language": "go", "intent": "add", "confidence": 0.9,
		},
	})
	action, err := DecodeStageAction("submit_code", args)
	if err != nil {
		t.Fatal(err)
	}
	if action.ProposalArgs == "" || action.Request != nil {
		t.Fatalf("submit action wrong: %+v", action)
	}
}

// TestDecodeEnvelopeRejectsBothAndNeither pins the discriminated-union
// rules: both actions present is rejected WITHOUT side effects; neither
// is malformed; a bare files payload (pre-envelope model output) decodes
// as submit_changes for backward compatibility.
func TestDecodeEnvelopeRejectsBothAndNeither(t *testing.T) {
	both := dEnvelope(t, map[string]any{
		"request_context": map[string]any{"reason": "r", "queries": []map[string]any{
			{"query_type": "read_file", "path": "a.go", "max_results": 1, "max_chars": 100},
		}},
		"submit_changes": map[string]any{
			"files":    []map[string]any{{"path": "a.go", "change_type": "create", "content": "x"}},
			"language": "go", "intent": "i", "confidence": 0.9,
		},
	})
	if _, err := DecodeStageAction("submit_code", both); err == nil {
		t.Fatal("context + edits in one action must be rejected without side effects")
	}
	neither := dEnvelope(t, map[string]any{"language": "go", "intent": "i", "confidence": 0.9})
	if _, err := DecodeStageAction("submit_code", neither); err == nil {
		t.Fatal("neither action must be malformed")
	}
	// Bare files payload (no envelope): legacy submit path via the
	// stream probe, which carries the backward-compatibility fallback.
	bare := dEnvelope(t, map[string]any{
		"files":    []map[string]any{{"path": "a.go", "change_type": "create", "content": "x"}},
		"language": "go", "intent": "i", "confidence": 0.9,
	})
	action, err := TryDecodeStageAction("submit_code", dStream(bare))
	if err != nil {
		t.Fatalf("bare payload must decode as submit_changes: %v", err)
	}
	if action.ProposalArgs == "" || action.Request != nil {
		t.Fatalf("bare payload action wrong: %+v", action)
	}
}

// TestActionContextRequestBoundsAndQueryShapes pins the host validation:
// query count, aggregate bytes, supported shapes; a repository-wide
// listing is not an expansion query and there are no shell commands.
func TestActionContextRequestBoundsAndQueryShapes(t *testing.T) {
	five := map[string]any{"reason": "r"}
	queries := []map[string]any{}
	for _, p := range []string{"a", "b", "c", "d", "e"} {
		queries = append(queries, map[string]any{"query_type": "read_file", "path": p + ".go", "max_results": 1, "max_chars": 100})
	}
	five["queries"] = queries
	if _, err := DecodeStageAction("submit_code", dEnvelope(t, map[string]any{"request_context": five})); err == nil {
		t.Fatal("5 queries must exceed the action bound")
	}
	shell := dEnvelope(t, map[string]any{"request_context": map[string]any{
		"reason":  "r",
		"queries": []map[string]any{{"query_type": "run_command", "path": ".", "max_results": 1, "max_chars": 100}},
	}})
	if _, err := DecodeStageAction("submit_code", shell); err == nil {
		t.Fatal("shell-shaped queries must be rejected (no shell commands)")
	}
}

// clampFixture returns a read_file request whose per-query MaxChars are maxChars.
func clampFixture(maxChars ...int) schemas.ContextRequest {
	req := schemas.ContextRequest{Reason: "needs source"}
	for i, n := range maxChars {
		req.Queries = append(req.Queries, schemas.ContextQuery{
			QueryType:  schemas.ContextReadFile,
			Path:       strPtr(fmt.Sprintf("internal/pkg/file%d.go", i)),
			MaxResults: 10,
			MaxChars:   n,
		})
	}
	return req
}

// TestClampActionContextRequestFitsBound pins that an oversized request is
// scaled down to the bound and then passes the action validator.
func TestClampActionContextRequestFitsBound(t *testing.T) {
	req := clampFixture(18000, 18000, 18000, 18000) // sums to 72000; each query within the 20000 per-query bound
	got := clampActionContextRequest(req)
	total := 0
	for _, q := range got.Queries {
		total += q.MaxChars
	}
	if total > maxActionContextBytes {
		t.Fatalf("clamped sum = %d, want <= %d", total, maxActionContextBytes)
	}
	if err := ValidateActionContextRequest(got); err != nil {
		t.Fatalf("clamped request rejected: %v", err)
	}
}

// TestClampActionContextRequestUnderBoundUnchanged pins that a request within
// the bound is returned as-is.
func TestClampActionContextRequestUnderBoundUnchanged(t *testing.T) {
	req := clampFixture(20000, 20000, 8000) // sums to 48000, exactly the bound
	if got := clampActionContextRequest(req); !reflect.DeepEqual(got, req) {
		t.Fatalf("request under bound changed: %+v", got)
	}
}

// TestClampActionContextRequestKeepsProportions pins that the largest query
// stays the largest after scaling.
func TestClampActionContextRequestKeepsProportions(t *testing.T) {
	got := clampActionContextRequest(clampFixture(20000, 15000, 10000, 9000)) // sums to 54000
	for i := 1; i < len(got.Queries); i++ {
		if got.Queries[i-1].MaxChars <= got.Queries[i].MaxChars {
			t.Fatalf("proportions lost at %d: %+v", i, got.Queries)
		}
	}
}

// TestDecodeStageActionClampsOversizedContextRequest pins that the decode path
// uses the clamped request, so a model that asks for 54000 chars is not
// rejected.
func TestDecodeStageActionClampsOversizedContextRequest(t *testing.T) {
	payload := dEnvelope(t, map[string]any{"request_context": map[string]any{
		"reason": "r",
		"queries": []map[string]any{
			{"query_type": "read_file", "path": "a.go", "max_results": 1, "max_chars": 18000},
			{"query_type": "read_file", "path": "b.go", "max_results": 1, "max_chars": 18000},
			{"query_type": "read_file", "path": "c.go", "max_results": 1, "max_chars": 18000},
		},
	}})
	action, err := DecodeStageAction("submit_code", payload)
	if err != nil {
		t.Fatalf("oversized request must be clamped, got error: %v", err)
	}
	if action.Request == nil {
		t.Fatal("decoded action carries no request")
	}
	total := 0
	for _, q := range action.Request.Queries {
		total += q.MaxChars
	}
	if total > maxActionContextBytes {
		t.Fatalf("decoded request sums to %d, want <= %d", total, maxActionContextBytes)
	}
}

// TestTryDecodeFromCollectedStream pins the stream-level probe: the
// envelope rides the forced tool call, and missing tool calls error.
func TestTryDecodeFromCollectedStream(t *testing.T) {
	args := dEnvelope(t, map[string]any{
		"request_context": map[string]any{"reason": "r", "queries": []map[string]any{
			{"query_type": "read_file", "path": "a.go", "max_results": 1, "max_chars": 100},
		}},
	})
	action, err := TryDecodeStageAction("submit_code", dStream(args))
	if err != nil {
		t.Fatal(err)
	}
	if action.Request == nil {
		t.Fatalf("request lost in stream decode: %+v", action)
	}
	if _, err := TryDecodeStageAction("submit_code", &zeroruntime.CollectedStream{}); err == nil {
		t.Fatal("a stream without the tool call must error")
	}
}

// TestDeclaredActionConflictPropagates proves the backward-compat fallback
// does not swallow a declared-action conflict. A payload carrying both
// action=submit_changes and a request_context must fail loud.
func TestDeclaredActionConflictPropagates(t *testing.T) {
	args := `{"action":"submit_changes","request_context":{"reason":"x","queries":[{"query_type":"read_file","path":"y","max_results":1,"max_chars":100}]},"files":[{"path":"x","change_type":"modify","base_ref":"h","edits":[{"old":"a","new":"b"}]}]}`
	_, err := DecodeStageAction("submit_code", args)
	if err == nil {
		t.Fatal("action=submit_changes with request_context and files decoded, want a loud rejection for mutual exclusivity")
	}
}

// TestDeclaredActionConflictWithActionFieldPropagates proves that even
// when a conflicting payload has a files array, the presence of the action
// field means the error propagates rather than being silently downgraded.
func TestDeclaredActionConflictWithActionFieldPropagates(t *testing.T) {
	args := `{"action":"request_context","submit_changes":{"files":[]},"request_context":{"reason":"x","queries":[{"query_type":"read_file","path":"y","max_results":1,"max_chars":100}]}}`
	_, err := DecodeStageAction("submit_code", args)
	if err == nil {
		t.Fatal("action=request_context with submit_changes and request_context decoded, want a loud rejection")
	}
}

// TestSubmissionRepairsStringifiedFiles pins the D0-campaign quirk:
// deepseek-v4.1-flash sent "files" as a string that contains the array.
func TestSubmissionRepairsStringifiedFiles(t *testing.T) {
	files := `[{"path":"main.go","change_type":"modify","content":"package main\n"}]`
	quoted, _ := json.Marshal(files)
	args := `{"files":` + string(quoted) + `,"language":"go","intent":"add const","confidence":0.9}`
	action, err := decodeSubmissionCall("submit_code", args)
	if err != nil {
		t.Fatalf("a stringified files array must be repaired, got %v", err)
	}
	out, err := parseCodeWriterArgs(action.ProposalArgs)
	if err != nil {
		t.Fatalf("the repaired arguments must parse as a code writer submission: %v", err)
	}
	if len(out.Files) != 1 || out.Files[0].Path != "main.go" {
		t.Fatalf("files = %+v", out.Files)
	}
	if _, err := decodeSubmissionCall("submit_code", `{"files":"not an array","language":"go"}`); err == nil {
		t.Fatal("a string that is not a JSON array must still fail")
	}
}
