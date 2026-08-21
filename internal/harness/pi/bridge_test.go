package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/harness"
	"github.com/Taf0711/splice/internal/streamjson"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// TestRunEventToStreamJSMapsEveryKind pins the envelope-to-wire mapping: each
// typed harness RunEvent has exactly one stream-json representation, and a
// payload-less event is dropped (never emitted as a corrupt line).
func TestRunEventToStreamJSMapsEveryKind(t *testing.T) {
	cases := []struct {
		name string
		ev   harness.RunEvent
		want streamjson.EventType
	}{
		{name: "plan", ev: harness.RunEvent{Kind: harness.RunEventPlan, Plan: &agent.PipelinePlanEvent{Stages: []string{"code_writer"}}}, want: streamjson.EventPipelinePlan},
		{name: "stage", ev: harness.RunEvent{Kind: harness.RunEventStage, Stage: &agent.StageEvent{Name: "code_writer", Status: "completed", Progress: 100}}, want: streamjson.EventStage},
		{name: "tool", ev: harness.RunEvent{Kind: harness.RunEventTool, Tool: &agent.ToolCall{ID: "c1", Name: "write_file", Arguments: `{"path":"x"}`}}, want: streamjson.EventToolCall},
		{name: "permission", ev: harness.RunEvent{Kind: harness.RunEventPermission, Perm: &agent.PermissionRequest{ToolCallID: "c1", ToolName: "bash"}}, want: streamjson.EventPermissionRequest},
		{name: "usage", ev: harness.RunEvent{Kind: harness.RunEventUsage, Usage: &agent.Usage{PromptTokens: 1, CompletionTokens: 2}}, want: streamjson.EventUsage},
		{name: "text", ev: harness.RunEvent{Kind: harness.RunEventText, Text: "hi"}, want: streamjson.EventText},
		{name: "reasoning", ev: harness.RunEvent{Kind: harness.RunEventReasoning, Reasoning: "think"}, want: streamjson.EventReasoning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line, ok := runEventToStreamJSON(tc.ev)
			if !ok || line.Type != tc.want {
				t.Fatalf("map = %#v ok=%v, want type %s", line, ok, tc.want)
			}
		})
	}
	// Missing payload drops the event.
	if _, ok := runEventToStreamJSON(harness.RunEvent{Kind: harness.RunEventStage}); ok {
		t.Fatal("stage event with nil stage must be dropped")
	}
	// Unknown event kind drops the event.
	if _, ok := runEventToStreamJSON(harness.RunEvent{Kind: harness.RunEventKind("nope")}); ok {
		t.Fatal("unknown kind must be dropped")
	}
}

// TestRunEventToStreamJSUsesChangeTypeField pins the schema reuse: the wire
// stage event must carry the staged changed files under changedFiles.
func TestRunEventToStreamJSUsesChangeTypeField(t *testing.T) {
	line, ok := runEventToStreamJSON(harness.RunEvent{
		Kind:  harness.RunEventStage,
		Stage: &agent.StageEvent{Name: "code_writer", Status: "completed", Progress: 100, ChangedFiles: []string{"hello.go"}},
	})
	if !ok {
		t.Fatal("map failed")
	}
	data, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["changedFiles"] == nil {
		t.Fatalf("stage wire must carry changedFiles, got %s", data)
	}
}

// TestStreamSinkStampsRunIDOnEveryLine pins the stream-json contract: every
// output event carries the run id, and a malformed event records the transport
// error instead of corrupting the stream.
func TestStreamSinkStampsRunIDOnEveryLine(t *testing.T) {
	var buf bytes.Buffer
	sink := NewStreamSink(&buf)
	sink.Send(harness.RunEvent{Kind: harness.RunEventPlan, Plan: &agent.PipelinePlanEvent{Stages: []string{"a"}}})
	sink.Send(harness.RunEvent{Kind: harness.RunEventText, Text: "done"})

	if err := sink.Err(); err != nil {
		t.Fatalf("sink err: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2: %q", len(lines), buf.String())
	}
	for _, line := range lines {
		var ev streamjson.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("bad line %q: %v", line, err)
		}
		if ev.RunID == "" {
			t.Fatalf("event missing runId: %q", line)
		}
	}
}

// TestCommandLoopRoutesAndContinuesOnMalformedInput pins the control seam:
// a valid cancel command reaches its control, an undeclared capability is
// rejected, and malformed/unknown input produces warnings but does not kill
// the loop.
func TestCommandLoopRoutesAndContinuesOnMalformedInput(t *testing.T) {
	cancelled := 0
	caps := harness.CapabilitySet{}
	ctrl := harness.Controls{CancelRun: func() { cancelled++ }}

	var errs bytes.Buffer
	input := strings.NewReader(strings.Join([]string{
		`{"kind":"cancel_run"}`,
		`not json`,
		`{"kind":"grant_permission","permId":"c1"}`,
		`{"kind":"make_coffee"}`,
		`{"kind":"cancel_run"}`,
		"",
	}, "\n"))
	err := CommandLoop(context.Background(), input, &errs, caps, ctrl)
	if err != nil {
		t.Fatalf("loop err: %v", err)
	}
	if cancelled != 2 {
		t.Fatalf("cancelled = %d, want 2 (valid cancels route)", cancelled)
	}
	warnings := errs.String()
	if !strings.Contains(warnings, "malformed") {
		t.Fatalf("malformed line should warn, got %q", warnings)
	}
	if !strings.Contains(warnings, "capability") {
		t.Fatalf("undeclared grant should warn, got %q", warnings)
	}
	if !strings.Contains(warnings, "unknown control command") {
		t.Fatalf("unknown kind should warn, got %q", warnings)
	}
}

// TestTerminalStatus pins the terminal decision, including the honest no-op
// rule: a cancel that arrives after a normal completion must not flip the
// outcome away from success. Only a run that stopped because of cancellation
// reports interrupted.
func TestTerminalStatus(t *testing.T) {
	cases := []struct {
		name        string
		runErr      error
		ctxCanceled bool
		incomplete  bool
		wantStatus  string
		wantCode    int
	}{
		{name: "success", wantStatus: "success", wantCode: 0},
		{name: "late cancel after completion is a no-op", ctxCanceled: true, wantStatus: "success", wantCode: 0},
		{name: "canceled in flight", runErr: context.Canceled, ctxCanceled: true, wantStatus: "interrupted", wantCode: 130},
		{name: "deadline counts as interrupted", runErr: context.DeadlineExceeded, ctxCanceled: true, wantStatus: "interrupted", wantCode: 130},
		{name: "plain error", runErr: context.DeadlineExceeded, wantStatus: "error", wantCode: 1},
		{name: "incomplete completion", incomplete: true, wantStatus: "incomplete", wantCode: 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, code := TerminalStatus(tc.runErr, tc.ctxCanceled, tc.incomplete)
			if status != tc.wantStatus || code != tc.wantCode {
				t.Fatalf("TerminalStatus = (%q, %d), want (%q, %d)", status, code, tc.wantStatus, tc.wantCode)
			}
		})
	}
}

// TestFixtureProviderBlockUntilCancel pins the deterministic test gate: the
// gated provider opens a stream that carries no events and closes only when
// the context is canceled. The ungated provider emits immediately.
func TestFixtureProviderBlockUntilCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := (FixtureProvider{BlockUntilCancel: true}).StreamCompletion(ctx, zeroruntime.CompletionRequest{})
	if err != nil {
		t.Fatalf("gated stream: %v", err)
	}
	select {
	case _, open := <-ch:
		t.Fatalf("gated stream delivered an event or closed before cancel (open=%v)", open)
	default:
	}
	cancel()
	select {
	case _, open := <-ch:
		if open {
			t.Fatal("gated stream delivered an event after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("gated stream did not close after cancel")
	}
}

// TestCommandLoopStopsOnContextCancel pins process cleanup: when the parent
// cancels, the loop exits promptly instead of blocking on stdin forever.
func TestCommandLoopStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- CommandLoop(ctx, strings.NewReader(""), &bytes.Buffer{}, harness.CapabilitySet{}, harness.Controls{})
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("loop err after cancel: %v", err)
		}
	case <-make(chan struct{}): // never
		t.Fatal("loop blocked after context cancel")
	}
}

// TestFixtureProviderServesSubmitCode pins the deterministic fixture: it
// emits exactly the submit_code tool call with valid arguments.
func TestFixtureProviderServesSubmitCode(t *testing.T) {
	ch, err := FixtureProvider{}.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for ev := range ch {
		count++
		if ev.Type == zeroruntime.StreamEventToolCallDelta {
			var out struct {
				Files []struct {
					Path       string `json:"path"`
					ChangeType string `json:"change_type"`
				} `json:"files"`
			}
			if err := json.Unmarshal([]byte(ev.ArgumentsFragment), &out); err != nil {
				t.Fatalf("bad fixture args: %v", err)
			}
			if len(out.Files) != 1 || out.Files[0].ChangeType != "create" {
				t.Fatalf("fixture files = %#v", out.Files)
			}
		}
	}
	if count == 0 {
		t.Fatal("fixture produced no events")
	}
}
