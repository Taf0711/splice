package harness

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
)

// recordingSink captures RunEvent values for assertions.
type recordingSink struct {
	events []RunEvent
}

func (s *recordingSink) Send(event RunEvent) {
	s.events = append(s.events, event)
}

// TestRunEventValidation pins the kind/payload pairing contract: an event with
// a payload that does not match its kind must fail validation.
func TestRunEventValidation(t *testing.T) {
	plan := &agent.PipelinePlanEvent{Stages: []string{"code_writer", "test_runner"}}
	stage := &agent.StageEvent{Name: "code_writer", Status: "completed", Progress: 100}
	if err := (RunEvent{Kind: RunEventPlan, Plan: plan}).Validate(); err != nil {
		t.Fatalf("valid plan event rejected: %v", err)
	}
	if err := (RunEvent{Kind: RunEventStage, Stage: stage}).Validate(); err != nil {
		t.Fatalf("valid stage event rejected: %v", err)
	}
	if err := (RunEvent{Kind: RunEventPlan, Stage: stage}).Validate(); err == nil {
		t.Fatal("plan event with a stage payload must fail validation")
	}
	if err := (RunEvent{Kind: RunEventStage}).Validate(); err == nil {
		t.Fatal("stage event with no payload must fail validation")
	}
	if err := (RunEvent{Kind: "nope"}).Validate(); err == nil {
		t.Fatal("unknown kind must fail validation")
	}
	if err := (RunEvent{Kind: RunEventText, Text: "hi"}).Validate(); err != nil {
		t.Fatalf("text event rejected: %v", err)
	}
}

// TestControlCommandValidation pins the command kind/payload pairing.
func TestControlCommandValidation(t *testing.T) {
	if err := (ControlCommand{Kind: CtrlSetModel, Model: "gpt-4.1"}).Validate(); err != nil {
		t.Fatalf("valid set_model rejected: %v", err)
	}
	if err := (ControlCommand{Kind: CtrlSetModel}).Validate(); err == nil {
		t.Fatal("set_model with no model must fail")
	}
	if err := (ControlCommand{Kind: CtrlGrantPermit, PermID: "call_1"}).Validate(); err != nil {
		t.Fatalf("valid grant rejected: %v", err)
	}
	if err := (ControlCommand{Kind: CtrlDenyPermit}).Validate(); err == nil {
		t.Fatal("deny with no permission id must fail")
	}
	if err := (ControlCommand{Kind: CtrlCancelRun, PermID: "call_1"}).Validate(); err == nil {
		t.Fatal("cancel with a payload must fail")
	}
	if err := (ControlCommand{Kind: "nope"}).Validate(); err == nil {
		t.Fatal("unknown command kind must fail")
	}
}

// TestCapabilitySetGatesPermissionCommands pins the capability/authority split:
// a permission decision is a SURFACE the harness must declare. A harness that
// does not declare approvals cannot route any permission command, regardless
// of what the core would otherwise allow.
func TestCapabilitySetGatesPermissionCommands(t *testing.T) {
	noApprovals := CapabilitySet{}
	if noApprovals.Requires(ControlCommand{Kind: CtrlGrantPermit, PermID: "call_1"}) {
		t.Fatal("undeclared approvals capability must not permit grant")
	}
	withApprovals := CapabilitySet{ShowApprovals: true}
	if !withApprovals.Requires(ControlCommand{Kind: CtrlGrantPermit, PermID: "call_1"}) {
		t.Fatal("declared approvals capability must permit grant")
	}
	if withApprovals.Requires(ControlCommand{Kind: CtrlSetModel, Model: "x"}) {
		t.Fatal("undeclared set_model capability must not permit set_model")
	}
}

// TestCapabilityDistinctFromAuthority pins the naming contract: capability
// bits are surface descriptors, never authority. Authority lives in the
// permission mode and the sandbox, which this package does not expose. The
// set carries only the surface bits the command gates read; no authority
// fields (permission mode, autonomy, sandbox grant) exist on the type.
func TestCapabilityDistinctFromAuthority(t *testing.T) {
	caps := CapabilitySet{ShowApprovals: true}
	for _, c := range []ControlCommand{
		{Kind: CtrlGrantPermit, PermID: "x"},
		{Kind: CtrlDenyPermit, PermID: "x"},
	} {
		if !caps.Requires(c) {
			t.Fatalf("capability set should gate %s as a surface", c.Kind)
		}
	}
	if caps.SetModel {
		t.Fatal("set_model must not be declared by an approvals-only set")
	}
}

// TestWireForwardsTypedEventsPairsProducerToConsumer pins the producer/consumer
// pairing: every callback the orchestrator emits must arrive at the sink as a
// typed RunEvent with the same payload type. This is the pairing test for the
// event side of the contract.
func TestWireForwardsTypedEventsPairsProducerToConsumer(t *testing.T) {
	sink := &recordingSink{}
	opts := agent.Options{}
	wired := Wire(opts, sink)

	wired.OnPipelinePlan(agent.PipelinePlanEvent{Stages: []string{"code_writer", "test_runner"}})
	wired.OnStageEvent(agent.StageEvent{Name: "code_writer", Status: "completed", Progress: 100})
	wired.OnToolCall(agent.ToolCall{ID: "call_1", Name: "read_file"})
	wired.OnUsage(agent.Usage{PromptTokens: 1, CompletionTokens: 2})
	wired.OnText("hello")
	wired.OnReasoning("thinking")

	if len(sink.events) != 6 {
		t.Fatalf("sink received %d events, want 6", len(sink.events))
	}
	if sink.events[0].Kind != RunEventPlan || sink.events[0].Plan == nil || strings.Join(sink.events[0].Plan.Stages, ",") != "code_writer,test_runner" {
		t.Fatalf("plan event = %#v", sink.events[0])
	}
	if sink.events[1].Kind != RunEventStage || sink.events[1].Stage == nil || sink.events[1].Stage.Status != "completed" {
		t.Fatalf("stage event = %#v", sink.events[1])
	}
	if sink.events[2].Kind != RunEventTool || sink.events[2].Tool == nil || sink.events[2].Tool.Name != "read_file" {
		t.Fatalf("tool event = %#v", sink.events[2])
	}
	if sink.events[3].Kind != RunEventUsage || sink.events[3].Usage == nil || sink.events[3].Usage.TotalTokens() != 3 {
		t.Fatalf("usage event = %#v", sink.events[3])
	}
	if sink.events[4].Kind != RunEventText || sink.events[4].Text != "hello" {
		t.Fatalf("text event = %#v", sink.events[4])
	}
	if sink.events[5].Kind != RunEventReasoning || sink.events[5].Reasoning != "thinking" {
		t.Fatalf("reasoning event = %#v", sink.events[5])
	}
	for _, event := range sink.events {
		if err := event.Validate(); err != nil {
			t.Fatalf("sink event %v invalid: %v", event.Kind, err)
		}
	}
}

// TestWirePreservesPriorCallbacks pins the adapter rule: the seam must wrap,
// never replace. A harness attached to options that already have a consumer
// (the TUI or exec) must not silence it.
func TestWirePreservesPriorCallbacks(t *testing.T) {
	sink := &recordingSink{}
	priorStageCalls := 0
	priorPermCalls := 0
	opts := agent.Options{
		OnStageEvent: func(agent.StageEvent) { priorStageCalls++ },
		OnPermissionRequest: func(context.Context, agent.PermissionRequest) (agent.PermissionDecision, error) {
			priorPermCalls++
			return agent.PermissionDecision{Action: agent.PermissionDecisionAllow}, nil
		},
	}
	wired := Wire(opts, sink)

	wired.OnStageEvent(agent.StageEvent{Name: "x"})
	if priorStageCalls != 1 || len(sink.events) != 1 {
		t.Fatalf("prior stage callback silenced: prior=%d sink=%d", priorStageCalls, len(sink.events))
	}

	decision, err := wired.OnPermissionRequest(context.Background(), agent.PermissionRequest{ToolCallID: "c1"})
	if err != nil || decision.Action != agent.PermissionDecisionAllow {
		t.Fatalf("prior permission responder silenced: %+v err=%v", decision, err)
	}
	if priorPermCalls != 1 || len(sink.events) != 2 {
		t.Fatalf("permission pairing broken: prior=%d sink=%d", priorPermCalls, len(sink.events))
	}
	if sink.events[1].Kind != RunEventPermission || sink.events[1].Perm == nil {
		t.Fatalf("permission event = %#v", sink.events[1])
	}
}

// TestWireDefaultsPermissionToDeny pins the fail-closed contract: with no
// prior responder the wired options deny, matching the core's default.
func TestWireDefaultsPermissionToDeny(t *testing.T) {
	wired := Wire(agent.Options{}, &recordingSink{})
	decision, err := wired.OnPermissionRequest(context.Background(), agent.PermissionRequest{ToolCallID: "c1"})
	if err != nil {
		t.Fatalf("permission default error: %v", err)
	}
	if decision.Action != agent.PermissionDecisionDeny {
		t.Fatalf("permission default = %s, want deny", decision.Action)
	}
}

// TestRouteRunsDeclaredControls pins the command/control pairing: each command
// kind routes to exactly its control, and an unavailable control rejects the
// command before any core effect.
func TestRouteRunsDeclaredControls(t *testing.T) {
	cancelled := false
	paused := false
	resumed := false
	setModel := ""
	var resolvedID string
	var resolvedAction agent.PermissionDecisionAction
	ctrl := Controls{
		CancelRun: func() { cancelled = true },
		PauseRun:  func() { paused = true },
		ResumeRun: func() { resumed = true },
		SetModel:  func(model string) error { setModel = model; return nil },
		ResolvePermission: func(id string, action agent.PermissionDecisionAction) error {
			resolvedID = id
			resolvedAction = action
			return nil
		},
	}
	caps := CapabilitySet{SetModel: true, ShowApprovals: true}

	if err := Route(ControlCommand{Kind: CtrlCancelRun}, caps, ctrl); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !cancelled {
		t.Fatal("cancel command did not reach its control")
	}
	if err := Route(ControlCommand{Kind: CtrlPauseRun}, caps, ctrl); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if !paused {
		t.Fatal("pause command did not reach its control")
	}
	if err := Route(ControlCommand{Kind: CtrlResumeRun}, caps, ctrl); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !resumed {
		t.Fatal("resume command did not reach its control")
	}
	if err := Route(ControlCommand{Kind: CtrlSetModel, Model: "gpt-4.1"}, caps, ctrl); err != nil {
		t.Fatalf("set_model: %v", err)
	}
	if setModel != "gpt-4.1" {
		t.Fatalf("set_model control got %q", setModel)
	}
	if err := Route(ControlCommand{Kind: CtrlGrantPermit, PermID: "call_9"}, caps, ctrl); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if resolvedID != "call_9" || resolvedAction != agent.PermissionDecisionAllow {
		t.Fatalf("grant resolved id=%q action=%s", resolvedID, resolvedAction)
	}
	if err := Route(ControlCommand{Kind: CtrlDenyPermit, PermID: "call_9"}, caps, ctrl); err != nil {
		t.Fatalf("deny: %v", err)
	}
	if resolvedID != "call_9" || resolvedAction != agent.PermissionDecisionDeny {
		t.Fatalf("deny resolved id=%q action=%s", resolvedID, resolvedAction)
	}
}

// TestRouteRejectsUndeclaredAndUnavailable pins the two guards: an undeclared
// capability rejects the command, and a nil control rejects it. Neither guard
// may reach the core.
func TestRouteRejectsUndeclaredAndUnavailable(t *testing.T) {
	noCaps := CapabilitySet{}
	ctrl := Controls{SetModel: func(string) error { return nil }, ResolvePermission: func(string, agent.PermissionDecisionAction) error { return nil }}
	if err := Route(ControlCommand{Kind: CtrlSetModel, Model: "x"}, noCaps, ctrl); err == nil {
		t.Fatal("undeclared set_model must be rejected before routing")
	}
	if err := Route(ControlCommand{Kind: CtrlCancelRun}, CapabilitySet{}, Controls{}); err == nil {
		t.Fatal("cancel with no control must be rejected")
	}
	if err := Route(ControlCommand{Kind: CtrlSetModel, Model: "x"}, CapabilitySet{SetModel: true}, Controls{}); err == nil {
		t.Fatal("set_model with no control must be rejected")
	}
	if err := Route(ControlCommand{Kind: CtrlGrantPermit, PermID: "c1"}, CapabilitySet{ShowApprovals: true}, Controls{}); err == nil {
		t.Fatal("grant with no resolver must be rejected")
	}
}
