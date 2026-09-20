package presentation

import "fmt"

// Lifecycle names the phase of a presentation state. The empty value is the
// initial unstarted sentinel: a state with no event yet has no phase.
// Any non-empty value must be one of the closed set below.
//
// The set is the contract's 9-phase runtime lane (SPLICE_TUI v0.5 §2.1):
//
//	design -> crystallizing -> plan_ready -> critique -> awaiting_approval
//	  -> executing -> verifying -> merge_back -> complete
//
// Recovery is a Health state, not a phase: regression and intervention
// happen DURING executing, not after it.
type Lifecycle string

const (
	LifecycleDesign          Lifecycle = "design"
	LifecycleCrystallizing   Lifecycle = "crystallizing"
	LifecyclePlanReady       Lifecycle = "plan_ready"
	LifecycleCritique        Lifecycle = "critique"
	LifecycleAwaitingApprove Lifecycle = "awaiting_approval"
	LifecycleExecute         Lifecycle = "executing"
	LifecycleVerifying       Lifecycle = "verifying"
	LifecycleMergeBack       Lifecycle = "merge_back"
	LifecycleComplete        Lifecycle = "complete"
)

// Validate reports an error when the lifecycle is non-empty and not one of
// the known phases. The empty value is valid: it means no phase has been
// entered yet.
func (l Lifecycle) Validate() error {
	switch l {
	case "",
		LifecycleDesign,
		LifecycleCrystallizing,
		LifecyclePlanReady,
		LifecycleCritique,
		LifecycleAwaitingApprove,
		LifecycleExecute,
		LifecycleVerifying,
		LifecycleMergeBack,
		LifecycleComplete:
		return nil
	}
	return fmt.Errorf("unknown lifecycle %q", l)
}

// Health is the independent health dimension (v0.5 §2.3). A phase says where
// the run is; health says whether it is stuck, sick, or dead. The zero value
// is HealthNormal, so a state that never sets health projects "normal".
type Health string

const (
	HealthNormal        Health = "normal"
	HealthBlockedOnUser Health = "blocked_on_user"
	HealthRegression    Health = "regression"
	HealthRecovering    Health = "recovering"
	HealthFailed        Health = "failed"
	HealthCancelled     Health = "cancelled"
	HealthUntrusted     Health = "untrusted"
)

// Validate checks the closed health set. The empty value normalizes to
// normal: health is never unknown once a state exists.
func (h Health) Validate() error {
	switch h {
	case "", HealthNormal, HealthBlockedOnUser, HealthRegression,
		HealthRecovering, HealthFailed, HealthCancelled, HealthUntrusted:
		return nil
	}
	return fmt.Errorf("unknown health %q", h)
}

// Effective returns the normalized health: an unset health IS normal.
func (h Health) Effective() Health {
	if h == "" {
		return HealthNormal
	}
	return h
}

// GateKind is the closed set of blocking gate classes (v0.5 §2.3, GateView).
type GateKind string

const (
	GateAskUser     GateKind = "ask_user"
	GateApproval    GateKind = "approval"
	GateCritiqueBlk GateKind = "critique_block"
	GateTrust       GateKind = "trust"
)

// Validate checks the closed gate-kind set.
func (g GateKind) Validate() error {
	switch g {
	case GateAskUser, GateApproval, GateCritiqueBlk, GateTrust:
		return nil
	}
	return fmt.Errorf("unknown gate kind %q", g)
}

// GateView is the typed blocking-gate state. A gate is projected, never
// decided, by the renderer; the runtime owns whether progression is legal.
// Hard invariants the RUNTIME must uphold when a gate is active (the
// presentation only mirrors them):
//   - zero running agents;
//   - zero token burn while the gate waits.
type GateView struct {
	Kind     GateKind `json:"kind"`
	Prompt   string   `json:"prompt"`
	Blocking bool     `json:"blocking"`
}

// Validate checks kind and blocking invariants. A gate without a prompt
// gives the user nothing to act on, so the prompt is required; blocking
// gates are the only kind this contract carries (defer semantics live with
// the interaction layer, not in state).
func (g GateView) Validate() error {
	if err := g.Kind.Validate(); err != nil {
		return err
	}
	if !g.Blocking {
		return fmt.Errorf("gate %s: only blocking gates are representable; non-blocking notices are not gates", g.Kind)
	}
	return nil
}
