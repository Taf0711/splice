package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/tools"
)

// escalate_model is a control-only tool (SideEffectNone). The stream-json tool
// call must report sideEffect "none", not "unknown", so automation sees the
// promised value.
func TestStreamJSONPipelinePlanCarriesOrderedStageRoster(t *testing.T) {
	var stdout bytes.Buffer
	writer := execEventWriter{
		stdout:       &stdout,
		format:       execOutputStreamJSON,
		runID:        "run_test",
		streamedText: &strings.Builder{},
	}
	writer.pipelinePlan(agent.PipelinePlanEvent{Stages: []string{"code_writer", "test_runner"}})
	if writer.err != nil {
		t.Fatalf("writer: %v", writer.err)
	}
	events := decodeJSONLines(t, stdout.String())
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event["type"] != "pipeline_plan" {
		t.Fatalf("type = %#v, want pipeline_plan", event["type"])
	}
	stages, ok := event["stages"].([]any)
	if !ok || len(stages) != 2 || stages[0] != "code_writer" || stages[1] != "test_runner" {
		t.Fatalf("stages = %#v, want ordered roster", event["stages"])
	}
}

func TestStreamJSONStageEventIsTyped(t *testing.T) {
	var stdout bytes.Buffer
	writer := execEventWriter{
		stdout:       &stdout,
		format:       execOutputStreamJSON,
		runID:        "run_test",
		streamedText: &strings.Builder{},
	}
	writer.stage(agent.StageEvent{
		Name:         "code_writer",
		Status:       "completed",
		Detail:       "wrote main.go",
		Progress:     100,
		ChangedFiles: []string{"main.go"},
	})
	if writer.err != nil {
		t.Fatalf("writer: %v", writer.err)
	}
	events := decodeJSONLines(t, stdout.String())
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event["type"] != "stage" || event["name"] != "code_writer" || event["status"] != "completed" {
		t.Fatalf("unexpected stage event: %#v", event)
	}
	if event["reason"] != "wrote main.go" {
		t.Fatalf("detail = %#v", event["reason"])
	}
	if event["progress"] != float64(100) {
		t.Fatalf("progress = %#v", event["progress"])
	}
}

func TestStreamJSONSideEffectReportsNoneForControlTool(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.NewEscalateModelTool())
	if got := streamJSONSideEffect("escalate_model", registry); got != "none" {
		t.Fatalf("streamJSONSideEffect(escalate_model) = %q, want none", got)
	}
}

// TestStreamJSONToolCallDeltaEmitsOnlyNewFragments guards against the O(n^2)
// re-emission regression: toolCallStart must emit a single tool_call_start,
// and each toolCallDelta call must emit a tool_call_delta carrying ONLY its
// own fragment (never the cumulative arguments seen so far). No "tool_call"
// event may appear on this path — that type is reserved for real tool
// executions paired with a tool_result.
func TestStreamJSONToolCallDeltaEmitsOnlyNewFragments(t *testing.T) {
	var stdout bytes.Buffer
	writer := execEventWriter{
		stdout:       &stdout,
		format:       execOutputStreamJSON,
		runID:        "run_test",
		streamedText: &strings.Builder{},
	}

	fragments := []string{`{"path":`, `"main.go",`, `"content":"package main"}`}
	writer.toolCallStart("call_1", "write_file")
	for _, fragment := range fragments {
		writer.toolCallDelta("call_1", fragment)
	}
	if writer.err != nil {
		t.Fatalf("writer returned error: %v", writer.err)
	}

	events := decodeJSONLines(t, stdout.String())
	if len(events) != 1+len(fragments) {
		t.Fatalf("expected %d events, got %d: %#v", 1+len(fragments), len(events), events)
	}

	start := events[0]
	if start["type"] != "tool_call_start" || start["id"] != "call_1" || start["name"] != "write_file" {
		t.Fatalf("unexpected start event: %#v", start)
	}
	if _, hasArgs := start["arguments"]; hasArgs {
		t.Fatalf("tool_call_start must not carry arguments, got %#v", start)
	}

	var concatenated strings.Builder
	for index, fragment := range fragments {
		event := events[index+1]
		if event["type"] != "tool_call_delta" || event["id"] != "call_1" {
			t.Fatalf("unexpected delta event %d: %#v", index, event)
		}
		delta, ok := event["delta"].(string)
		if !ok || delta != fragment {
			t.Fatalf("delta %d = %#v, want only new fragment %q (no cumulative re-emission)", index, event["delta"], fragment)
		}
		concatenated.WriteString(delta)
	}
	if got, want := concatenated.String(), strings.Join(fragments, ""); got != want {
		t.Fatalf("concatenated deltas = %q, want full arguments %q", got, want)
	}

	for _, event := range events {
		if event["type"] == "tool_call" {
			t.Fatalf("tool_call must not be emitted from the streaming path, got %#v", event)
		}
	}
}

// TestJSONToolCallDeltaEmitsOnlyNewFragments mirrors the stream-json guard
// above for the plain NDJSON (-o json) output format.
func TestJSONToolCallDeltaEmitsOnlyNewFragments(t *testing.T) {
	var stdout bytes.Buffer
	writer := execEventWriter{
		stdout:       &stdout,
		format:       execOutputJSON,
		streamedText: &strings.Builder{},
	}

	fragments := []string{`{"path":`, `"main.go",`, `"content":"package main"}`}
	writer.toolCallStart("call_1", "write_file")
	for _, fragment := range fragments {
		writer.toolCallDelta("call_1", fragment)
	}
	if writer.err != nil {
		t.Fatalf("writer returned error: %v", writer.err)
	}

	events := decodeJSONLines(t, stdout.String())
	if len(events) != 1+len(fragments) {
		t.Fatalf("expected %d events, got %d: %#v", 1+len(fragments), len(events), events)
	}
	if events[0]["type"] != "tool_call_start" || events[0]["name"] != "write_file" {
		t.Fatalf("unexpected start event: %#v", events[0])
	}

	var concatenated strings.Builder
	for index, fragment := range fragments {
		event := events[index+1]
		if event["type"] != "tool_call_delta" {
			t.Fatalf("unexpected delta event %d: %#v", index, event)
		}
		delta, _ := event["delta"].(string)
		if delta != fragment {
			t.Fatalf("delta %d = %#v, want only new fragment %q (no cumulative re-emission)", index, event["delta"], fragment)
		}
		concatenated.WriteString(delta)
	}
	if got, want := concatenated.String(), strings.Join(fragments, ""); got != want {
		t.Fatalf("concatenated deltas = %q, want full arguments %q", got, want)
	}
}
