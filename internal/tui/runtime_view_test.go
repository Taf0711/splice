package tui

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
)

// A prompt the runtime is blocked on is the gate: it projects as
// blocked_on_user with the matching kind, the status chip says NEEDS YOU,
// and the pipeline header shows the gate. Terminal health outranks it.
func TestRuntimeViewProjectsLiveGates(t *testing.T) {
	m := sidebarTestModel()
	if rv := m.runtimeView(); rv.gate != nil || rv.health != presentation.HealthNormal {
		t.Fatalf("idle runtime view = %+v, want no gate and normal health", rv)
	}

	cases := []struct {
		name string
		set  func(*model)
		kind presentation.GateKind
	}{
		{"ask_user", func(m *model) {
			m.pendingAskUser = &pendingAskUserPrompt{request: agent.AskUserRequest{Questions: []agent.AskUserQuestion{{Question: "which flag wins?"}}}}
		}, presentation.GateAskUser},
		{"permission", func(m *model) {
			m.pendingPermission = &pendingPermissionPrompt{request: agent.PermissionRequest{ToolName: "bash"}}
		}, presentation.GateApproval},
		{"trust", func(m *model) { m.trustPromptRequired = true }, presentation.GateTrust},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := sidebarTestModel()
			m.width = 140
			m.lastState = phaseChipState(presentation.LifecycleExecute, "", nil)
			m.pipeline.applyState(m.lastState)
			m.phaseTrail.observe(presentation.LifecycleExecute)
			tc.set(&m)
			rv := m.runtimeView()
			if rv.gate == nil || rv.gate.Kind != tc.kind || rv.health != presentation.HealthBlockedOnUser {
				t.Fatalf("runtime view = %+v gate=%+v, want %s + blocked_on_user", rv, rv.gate, tc.kind)
			}
			if chip := stripANSI(m.phaseChipSegment()); !strings.Contains(chip, "needs you") {
				t.Fatalf("status chip = %q, want NEEDS YOU", chip)
			}
			header := stripSidebar(m.renderContextSidebar(sidebarWidth(m.width), m.height))
			if !strings.Contains(strings.ToLower(header), "gate "+string(tc.kind)) {
				t.Fatalf("pipeline header missing gate %s:\n%s", tc.kind, header)
			}
		})
	}

	failed := sidebarTestModel()
	failed.lastState = phaseChipState(presentation.LifecycleComplete, presentation.HealthFailed, nil)
	failed.pendingPermission = &pendingPermissionPrompt{}
	if rv := failed.runtimeView(); rv.health != presentation.HealthFailed {
		t.Fatalf("terminal health overridden: %s", rv.health)
	}
}
