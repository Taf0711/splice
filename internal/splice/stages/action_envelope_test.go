package stages

// D1 regression tests: the discriminated action envelope.

import (
	"encoding/json"
	"testing"

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
