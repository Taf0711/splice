package pi

// Package pi bridges a Splice pipeline run to the Pi harness over the
// stream-json protocol. The bridge is an adapter, not a core change: it wires
// the existing agent.Options callbacks through the harness seam (RunEvent,
// ControlCommand, CapabilitySet) and transports them as stream-json lines.
//
// The Pi extension (pi-adapter/splice-bridge.ts) spawns this bridge as a child
// process, reads the stream-json events on stdout, and renders them with Pi's
// extension UI. The extension sends typed control commands on stdin; the
// bridge routes them through the harness CapabilitySet gate.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Taf0711/splice/internal/harness"
	"github.com/Taf0711/splice/internal/streamjson"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// StreamSink implements harness.Sink by emitting each RunEvent as a
// stream-json line. It reuses the documented stream-json protocol, so any
// headless client can consume the same events the bridge emits.
type StreamSink struct {
	w     io.Writer
	runID string
	mu    sync.Mutex
	err   error
}

// NewStreamSink returns a sink that writes stream-json lines to w. It
// generates one run id for all events, matching the stream-json contract that
// every output event carries the run id.
func NewStreamSink(w io.Writer) *StreamSink {
	runID, err := streamjson.CreateRunID(time.Now())
	if err != nil {
		runID = "run_pi_bridge"
	}
	return &StreamSink{w: w, runID: runID}
}

// Send marshals one RunEvent to a stream-json line.
func (s *StreamSink) Send(event harness.RunEvent) {
	line, ok := runEventToStreamJSON(event)
	if !ok {
		return
	}
	_ = s.write(line)
}

// Terminate writes the terminal run_end event. The stream-json contract makes
// the run_end exit code authoritative, so every bridge exit path emits one.
func (s *StreamSink) Terminate(status string, exitCode int) error {
	return s.write(streamjson.Event{Type: streamjson.EventRunEnd, Status: status, ExitCode: &exitCode})
}

// write stamps the sink run id and serializes one event. It is the single
// transport path for output and terminal events. A transport error is recorded
// and no further line is written after it.
func (s *StreamSink) write(line streamjson.Event) error {
	line.RunID = s.runID
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	data, err := streamjson.FormatEvent(line)
	if err != nil {
		s.err = err
		return err
	}
	_, s.err = io.WriteString(s.w, data+"\n")
	return s.err
}

// Err returns the first transport error, if any.
func (s *StreamSink) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// runEventToStreamJSON maps the harness RunEvent envelope onto the stream-json
// wire event. The mapping is pure and mirrors the exec writer's mapping. It
// returns false for events that have no stream-json representation.
func runEventToStreamJSON(event harness.RunEvent) (streamjson.Event, bool) {
	switch event.Kind {
	case harness.RunEventPlan:
		if event.Plan == nil {
			return streamjson.Event{}, false
		}
		return streamjson.Event{
			Type:   streamjson.EventPipelinePlan,
			Stages: append([]string(nil), event.Plan.Stages...),
		}, true
	case harness.RunEventStage:
		if event.Stage == nil {
			return streamjson.Event{}, false
		}
		progress := event.Stage.Progress
		return streamjson.Event{
			Type:         streamjson.EventStage,
			Name:         event.Stage.Name,
			Status:       event.Stage.Status,
			Reason:       event.Stage.Detail,
			Progress:     &progress,
			ChangedFiles: append([]string(nil), event.Stage.ChangedFiles...),
		}, true
	case harness.RunEventTool:
		if event.Tool == nil {
			return streamjson.Event{}, false
		}
		return streamjson.Event{
			Type: streamjson.EventToolCall,
			ID:   event.Tool.ID,
			Name: event.Tool.Name,
			Args: parseArgs(event.Tool.Arguments),
		}, true
	case harness.RunEventPermission:
		if event.Perm == nil {
			return streamjson.Event{}, false
		}
		req := event.Perm
		return streamjson.Event{
			Type:           streamjson.EventPermissionRequest,
			ID:             req.ToolCallID,
			Name:           req.ToolName,
			Action:         string(req.Action),
			Permission:     req.Permission,
			PermissionMode: string(req.PermissionMode),
			Autonomy:       req.Autonomy,
			SideEffect:     req.SideEffect,
			Reason:         req.Reason,
		}, true
	case harness.RunEventUsage:
		if event.Usage == nil {
			return streamjson.Event{}, false
		}
		usage := event.Usage
		prompt := usage.EffectiveInputTokens()
		completion := usage.EffectiveOutputTokens()
		total := usage.TotalTokens()
		return streamjson.Event{
			Type:             streamjson.EventUsage,
			PromptTokens:     &prompt,
			CompletionTokens: &completion,
			TotalTokens:      &total,
		}, true
	case harness.RunEventText:
		return streamjson.Event{Type: streamjson.EventText, Delta: event.Text}, true
	case harness.RunEventReasoning:
		return streamjson.Event{Type: streamjson.EventReasoning, Delta: event.Reasoning}, true
	case harness.RunEventFinal:
		return streamjson.Event{Type: streamjson.EventFinal, Text: event.Final}, true
	default:
		return streamjson.Event{}, false
	}
}

// TerminalStatus decides the bridge run_end status and exit code. A run that
// completed normally stays completed even when a cancel command arrives
// afterwards; only a run that stopped because of the cancellation reports
// interrupted.
func TerminalStatus(runErr error, ctxCanceled bool, incomplete bool) (string, int) {
	switch {
	case runErr != nil && ctxCanceled:
		return "interrupted", 130
	case runErr != nil:
		return "error", 1
	case incomplete:
		return "incomplete", 4
	default:
		return "success", 0
	}
}

func parseArgs(arguments string) any {
	if strings.TrimSpace(arguments) == "" {
		return nil
	}
	var args any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return arguments
	}
	return args
}

// CommandLine is the wire form of a ControlCommand as the Pi extension sends
// it on stdin. The bridge validates and routes it through the harness gate.
type CommandLine struct {
	Kind   string `json:"kind"`
	Model  string `json:"model,omitempty"`
	PermID string `json:"permId,omitempty"`
}

// CommandLoop reads CommandLine values from r and routes them through the
// harness CapabilitySet gate and Controls. It returns when the reader is
// exhausted or the context is done. A malformed line produces an error to
// errs (best-effort, stream-json warning) and the loop continues: one bad
// input must not kill the run.
func CommandLoop(ctx context.Context, r io.Reader, errs io.Writer, caps harness.CapabilitySet, ctrl harness.Controls) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var wire CommandLine
		if err := json.Unmarshal([]byte(line), &wire); err != nil {
			emitWarning(errs, fmt.Sprintf("bridge: malformed control line: %v", err))
			continue
		}
		cmd := harness.ControlCommand{
			Kind:   harness.ControlCommandKind(wire.Kind),
			Model:  wire.Model,
			PermID: wire.PermID,
		}
		if err := harness.Route(cmd, caps, ctrl); err != nil {
			emitWarning(errs, err.Error())
		}
	}
	return scanner.Err()
}

// emitWarning writes a stream-json warning line, best-effort. Warning lines
// need a run id to pass stream-json validation, so the bridge carries one.
func emitWarning(w io.Writer, message string) {
	if w == nil {
		return
	}
	timestamped, _ := streamjson.CreateRunID(time.Now())
	event, err := streamjson.FormatEvent(streamjson.Event{Type: streamjson.EventWarning, RunID: timestamped, Message: message})
	if err != nil {
		return
	}
	_, _ = io.WriteString(w, event+"\n")
}

// FixtureProvider is the deterministic provider for the bridge fixture. It
// serves exactly the submit_code tool call a TierTrivial run requests, so the
// fixture needs no live model and no Go toolchain.
//
// BlockUntilCancel turns the provider into a deterministic test gate: the
// stream stays open, with no events, until the caller cancels the context.
// The bridge process tests use it to guarantee a cancel command is routed
// while the run is in flight.
type FixtureProvider struct {
	BlockUntilCancel bool
}

// StreamCompletion implements agent.Provider for the fixture.
func (f FixtureProvider) StreamCompletion(ctx context.Context, _ zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	ch := make(chan zeroruntime.StreamEvent, 6)
	if f.BlockUntilCancel {
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return ch, nil
	}
	arguments := `{"files":[{"path":"hello.go","content":"package hello\n\n// Hello returns a greeting.\nfunc Hello() string { return \"hello\" }\n","change_type":"create"}],"language":"go","intent":"fix the spelling typo","confidence":0.95}`
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "fixture-1", ToolName: "submit_code"}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "fixture-1", ArgumentsFragment: arguments}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "fixture-1"}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 8, OutputTokens: 4}}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}
