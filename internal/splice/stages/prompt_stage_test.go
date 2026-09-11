package stages

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// promptRecordingProvider records the completion requests a prompt node sends
// and replays fixed text events.
type promptRecordingProvider struct {
	requests []zeroruntime.CompletionRequest
	events   []zeroruntime.StreamEvent
}

func (p *promptRecordingProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.requests = append(p.requests, request)
	ch := make(chan zeroruntime.StreamEvent, len(p.events))
	for _, event := range p.events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func textEvents(parts ...string) []zeroruntime.StreamEvent {
	events := make([]zeroruntime.StreamEvent, 0, len(parts)+1)
	for _, part := range parts {
		events = append(events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventText, Content: part})
	}
	return append(events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone})
}

// TestPromptStageRendersTemplateWithoutTools pins the documented context
// variables and that a prompt node sends no tools, so it cannot reach the
// workspace.
func TestPromptStageRendersTemplateWithoutTools(t *testing.T) {
	provider := &promptRecordingProvider{events: textEvents("done: summarized the module")}
	stage := PromptStage{Name: "note", Template: "intent={{intent}}\nsummaries={{summaries}}\nmemory={{memory}}"}
	input := newHarnessInput("summarize the module")
	input.PriorSummaries = map[string]string{"code_writer": "wrote files"}
	input.MemoryBundle = &schemas.MemoryBundle{
		RequestingAgent: "note",
		Observations:    []schemas.MemoryObservation{{Title: "gotcha", Content: "watch the nil case"}},
	}
	output, err := stage.Run(context.Background(), input, provider, StageOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(provider.requests))
	}
	request := provider.requests[0]
	if len(request.Tools) != 0 {
		t.Fatalf("prompt node sent %d tool(s), want none", len(request.Tools))
	}
	user := request.Messages[len(request.Messages)-1].Content
	if !strings.Contains(user, "summarize the module") {
		t.Fatalf("rendered prompt missing intent: %q", user)
	}
	if !strings.Contains(user, "code_writer: wrote files") {
		t.Fatalf("rendered prompt missing summaries: %q", user)
	}
	if !strings.Contains(user, "watch the nil case") {
		t.Fatalf("rendered prompt missing memory: %q", user)
	}
	if output.Summary != "done: summarized the module" {
		t.Fatalf("summary = %q, want the first line", output.Summary)
	}
	if output.Data["prompt_output"] != "done: summarized the module" {
		t.Fatalf("prompt_output = %v", output.Data["prompt_output"])
	}
	if stage.Capabilities().ModelFree {
		t.Fatal("a prompt node is model-backed")
	}
}

// TestPromptStageBoundsContext pins that the rendered summaries are capped.
func TestPromptStageBoundsContext(t *testing.T) {
	provider := &promptRecordingProvider{events: textEvents("ok")}
	stage := PromptStage{Name: "note", Template: "{{summaries}}"}
	input := newHarnessInput("x")
	input.PriorSummaries = map[string]string{
		"code_writer": strings.Repeat("a", maxPromptSummaryChars+500),
	}
	if _, err := stage.Run(context.Background(), input, provider, StageOptions{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	user := provider.requests[0].Messages[len(provider.requests[0].Messages)-1].Content
	if count := len([]rune(user)); count > maxPromptSummaryChars {
		t.Fatalf("rendered summaries = %d chars, want at most %d", count, maxPromptSummaryChars)
	}
}

// TestPromptStageFailsLoud pins the empty-template, missing-provider, and
// empty-answer refusals.
func TestPromptStageFailsLoud(t *testing.T) {
	ctx := context.Background()
	if _, err := (PromptStage{Name: "note"}).Run(ctx, newHarnessInput("x"), &promptRecordingProvider{}, StageOptions{}); err == nil || !strings.Contains(err.Error(), "template is empty") {
		t.Fatalf("empty template error = %v", err)
	}
	if _, err := (PromptStage{Name: "note", Template: "x"}).Run(ctx, newHarnessInput("x"), nil, StageOptions{}); err == nil || !strings.Contains(err.Error(), "provider is required") {
		t.Fatalf("missing provider error = %v", err)
	}
	provider := &promptRecordingProvider{events: textEvents("   ")}
	if _, err := (PromptStage{Name: "note", Template: "x"}).Run(ctx, newHarnessInput("x"), provider, StageOptions{}); err == nil || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("empty answer error = %v", err)
	}
}
