package tui

import (
	"strings"

	"github.com/Taf0711/splice/internal/presentation"
)

// runtimeView is the one phase/health/gate projection the chrome reads.
//
// Two sources feed it. The pipeline's presentation.State carries lifecycle
// and run health. The blocking gates live where the runtime blocks: an
// ask_user, permission, spec-review, or trust callback is waiting on the
// TUI for an answer, and the pending prompt IS that wait. Reading both here
// keeps every surface (status chip, pipeline header, compact strip) on the
// same answer instead of each one guessing.
type runtimeView struct {
	lifecycle presentation.Lifecycle
	health    presentation.Health
	gate      *presentation.GateView
}

func (m model) runtimeView() runtimeView {
	view := runtimeView{
		lifecycle: m.lastState.Lifecycle,
		health:    m.lastState.Health.Effective(),
		gate:      m.lastState.Gate,
	}
	if view.gate == nil {
		view.gate = m.liveGate()
	}
	// A blocking gate means the run waits on the user. Terminal health
	// (failed, cancelled) outranks it: a finished run is not blocked.
	if view.gate != nil && !healthTerminal(view.health) {
		view.health = presentation.HealthBlockedOnUser
	}
	return view
}

// liveGate projects the prompt the runtime is currently blocked on. Order
// follows what owns input: trust precedes everything, then the answer the
// run is waiting for.
func (m model) liveGate() *presentation.GateView {
	switch {
	case m.trustPromptRequired:
		return &presentation.GateView{Kind: presentation.GateTrust, Prompt: "trust this workspace config", Blocking: true}
	case m.pendingAskUser != nil:
		return &presentation.GateView{Kind: presentation.GateAskUser, Prompt: askUserGatePrompt(m.pendingAskUser), Blocking: true}
	case m.pendingSpecReview != nil:
		return &presentation.GateView{Kind: presentation.GateApproval, Prompt: "approve the spec", Blocking: true}
	case m.pendingPermission != nil:
		prompt := strings.TrimSpace(m.pendingPermission.request.ToolName)
		if prompt == "" {
			prompt = "tool call"
		}
		return &presentation.GateView{Kind: presentation.GateApproval, Prompt: "allow " + prompt, Blocking: true}
	}
	return nil
}

func askUserGatePrompt(p *pendingAskUserPrompt) string {
	questions := p.request.Questions
	if p.active >= 0 && p.active < len(questions) {
		if q := strings.TrimSpace(questions[p.active].Question); q != "" {
			return q
		}
	}
	if h := strings.TrimSpace(p.request.Header); h != "" {
		return h
	}
	return "question"
}

func healthTerminal(h presentation.Health) bool {
	return h == presentation.HealthFailed || h == presentation.HealthCancelled
}
