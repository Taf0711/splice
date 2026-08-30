package presentation

import (
	"fmt"
	"strings"
)

// EventKind discriminates the event classes the reducer understands. The
// classes map onto data the runtime already produces today: stage lifecycle
// events (agent.StageEvent: name, status, detail, progress, changed files),
// the pipeline roster announcement (agent.PipelinePlanEvent: stages), the
// repair loop's intervention messages (stage statuses "message"/"repaired"),
// and run termination.
type EventKind string

const (
	EventKindStage        EventKind = "stage"
	EventKindPlan         EventKind = "plan"
	EventKindRun          EventKind = "run"
	EventKindIntervention EventKind = "intervention"
)

// Event is the normalized event the reducer consumes. Its fields are chosen
// from the data the pipeline emits today, so a thin adapter can fill it from
// the runtime event types without modifying them. An unknown EventKind is an
// error, never a silent no-op.
type Event struct {
	Kind             EventKind
	NodeID           string
	NodeKind         NodeKind
	Status           string
	Detail           string
	Progress         int
	StageNames       []string
	Title            string
	InterventionKind InterventionKind
}

// StreamEventLike is the minimal event contract: anything that can project
// itself into the reducer's normalized Event vocabulary. It is one method,
// so any runtime event type can satisfy it with a thin adapter.
type StreamEventLike interface {
	// PresentationEvent normalizes the event into the reducer's vocabulary.
	PresentationEvent() Event
}

// StageEvent is the presentation projection of a stage lifecycle event. It
// carries the same data as the runtime's stage event: node name, kind (from
// the adapter), status, detail, and integer progress.
type StageEvent struct {
	ID       string
	Kind     NodeKind
	Status   string
	Detail   string
	Progress int
}

// PresentationEvent projects the stage event into the normalized form.
func (e StageEvent) PresentationEvent() Event {
	return Event{
		Kind:     EventKindStage,
		NodeID:   e.ID,
		NodeKind: e.Kind,
		Status:   e.Status,
		Detail:   e.Detail,
		Progress: e.Progress,
	}
}

// PlanEvent announces the ordered stage roster of one pipeline plan.
type PlanEvent struct {
	Title      string
	StageNames []string
}

// PresentationEvent projects the plan event into the normalized form.
func (e PlanEvent) PresentationEvent() Event {
	return Event{
		Kind:       EventKindPlan,
		StageNames: e.StageNames,
		Title:      e.Title,
	}
}

// RunEvent marks the end of a run.
type RunEvent struct {
	Status string // "completed" or "failed"
	Detail string
}

// PresentationEvent projects the run event into the normalized form.
func (e RunEvent) PresentationEvent() Event {
	return Event{Kind: EventKindRun, Status: e.Status, Detail: e.Detail}
}

// InterventionEvent proposes or applies one intervention against a node.
type InterventionEvent struct {
	Kind   InterventionKind
	NodeID string
	Status string // "proposed" or "applied"
	Reason string
}

// PresentationEvent projects the intervention event into the normalized form.
func (e InterventionEvent) PresentationEvent() Event {
	return Event{
		Kind:             EventKindIntervention,
		NodeID:           e.NodeID,
		Status:           e.Status,
		Detail:           e.Reason,
		InterventionKind: e.Kind,
	}
}

// Apply derives a new state from an event. It is pure: it never mutates the
// input state, reads no clocks, and touches no globals. Unknown or
// contradictory events return an error naming the event; silent no-ops are
// forbidden. The output always carries the current schema version and always
// passes Validate for legal event sequences.
func Apply(state State, event StreamEventLike) (State, error) {
	if event == nil {
		return State{}, fmt.Errorf("event is nil")
	}
	if state.SchemaVersion != 0 && state.SchemaVersion != PresentationSchemaVersionV1 {
		return State{}, fmt.Errorf("unsupported schema_version %d", state.SchemaVersion)
	}
	normalized := event.PresentationEvent()
	var (
		out State
		err error
	)
	switch normalized.Kind {
	case EventKindStage:
		out, err = applyStage(state, normalized)
	case EventKindPlan:
		out, err = applyPlan(state, normalized)
	case EventKindRun:
		out, err = applyRun(state, normalized)
	case EventKindIntervention:
		out, err = applyIntervention(state, normalized)
	default:
		return State{}, fmt.Errorf("unknown event kind %q", normalized.Kind)
	}
	if err != nil {
		return State{}, err
	}
	out.SchemaVersion = PresentationSchemaVersionV1
	return out, nil
}

// stageStatus maps a runtime stage status onto the closed node status set.
// "message" and "repaired" are intervention classes, not stage transitions,
// and are rejected here.
func stageStatus(status string) NodeStatus {
	switch status {
	case "running":
		return NodeStatusRunning
	case "completed":
		return NodeStatusComplete
	case "failed":
		return NodeStatusFailed
	case "skipped":
		return NodeStatusPending
	case "incomplete":
		return NodeStatusDegraded
	}
	return ""
}

func applyStage(state State, event Event) (State, error) {
	nodeID := strings.TrimSpace(event.NodeID)
	if nodeID == "" {
		return State{}, fmt.Errorf("stage event: node id is required")
	}
	status := stageStatus(event.Status)
	if status == "" {
		return State{}, fmt.Errorf("stage event for %s: unknown stage status %q", nodeID, event.Status)
	}
	progress := event.Progress
	if progress < 0 || progress > 100 {
		return State{}, fmt.Errorf("stage event for %s: progress must be within [0,100], got %d", nodeID, progress)
	}
	kind := event.NodeKind
	if err := kind.Validate(); err != nil {
		return State{}, fmt.Errorf("stage event for %s: %w", nodeID, err)
	}
	out := cloneState(state)
	idx := -1
	for i := range out.Nodes {
		if out.Nodes[i].ID == nodeID {
			idx = i
			break
		}
	}
	node := ExecutionNode{
		ID:       nodeID,
		Label:    nodeID,
		Kind:     kind,
		Status:   status,
		Progress: float64(progress) / 100,
	}
	if idx >= 0 {
		// Preserve per-node identity across updates. A terminal status may
		// regress to running: that is a repair re-entry, which is legal.
		prior := out.Nodes[idx]
		node.Iteration = prior.Iteration
		node.Cost = prior.Cost
		node.Usage = prior.Usage
		node.Dependencies = prior.Dependencies
		out.Nodes[idx] = node
	} else {
		out.Nodes = append(out.Nodes, node)
	}
	// Health is projection too: a failed stage is failed health; a repair
	// re-entry of the same stage clears it back to normal (the intervention
	// path owns recovering). Regression/refining semantics stay with the
	// trajectory layer, which sets them through interventions.
	if status == NodeStatusFailed {
		if out.Health.Effective() != HealthRecovering {
			out.Health = HealthFailed
		}
	} else if out.Health == HealthFailed && status == NodeStatusRunning {
		// The failed stage re-entered: the run is recovering.
		out.Health = HealthRecovering
	}
	out.Lifecycle = runPhase(out.Nodes)
	return out, nil
}

// applyPlan sets the plan and moves the run into executing. In the contract's
// runtime lane the orchestrator owns execution transitions: once the stage
// roster is announced, the run IS executing (the design/crystallize/critique
// phases belong to the design lane and are projected from design-mode runs).
func applyPlan(state State, event Event) (State, error) {
	if state.Lifecycle == LifecycleComplete {
		return State{}, fmt.Errorf("plan event after completion")
	}
	out := cloneState(state)
	out.Lifecycle = LifecycleExecute
	out.Plan = Plan{Title: event.Title, TaskCount: len(event.StageNames)}
	return out, nil
}

// runPhase derives the presentation phase from the running node set (v0.5
// lane: executing while code moves, verifying once only evidence stages
// remain). A node kind maps to the verify/analysis lane; write and test
// kinds are the execution lane. This is projection of runtime node truth,
// never a policy decision: the reducer only re-labels what the stage events
// already said.
func runPhase(nodes []ExecutionNode) Lifecycle {
	anyRunning := false
	verifyRunning := false
	execRunning := false
	for _, node := range nodes {
		switch node.Status {
		case NodeStatusRunning:
			anyRunning = true
			switch node.Kind {
			case NodeKindVerify, NodeKindAnalyze, NodeKindSecurity:
				verifyRunning = true
			default:
				execRunning = true
			}
		}
	}
	switch {
	case !anyRunning:
		return LifecycleExecute
	case execRunning:
		return LifecycleExecute
	case verifyRunning:
		return LifecycleVerifying
	}
	return LifecycleExecute
}

func applyRun(state State, event Event) (State, error) {
	status := event.Status
	if status != "completed" && status != "failed" && status != "cancelled" {
		return State{}, fmt.Errorf("run event: unknown run status %q", status)
	}
	if state.Lifecycle != LifecycleExecute && state.Lifecycle != LifecycleVerifying && state.Lifecycle != LifecycleMergeBack {
		return State{}, fmt.Errorf("run event: run finished while lifecycle is %q; completion requires execute, verify, or merge_back", state.Lifecycle)
	}
	out := cloneState(state)
	out.Lifecycle = LifecycleComplete
	switch status {
	case "completed":
		out.Health = ""
	case "failed":
		out.Health = HealthFailed
	case "cancelled":
		out.Health = HealthCancelled
	}
	out.Completion = &CompletionReceipt{Status: status, Detail: event.Detail}
	return out, nil
}

func applyIntervention(state State, event Event) (State, error) {
	kind := event.InterventionKind
	if err := kind.Validate(); err != nil {
		return State{}, fmt.Errorf("intervention event: %w", err)
	}
	status := event.Status
	if status != string(InterventionProposed) && status != string(InterventionApplied) {
		return State{}, fmt.Errorf("intervention event: unknown intervention status %q", status)
	}
	target := strings.TrimSpace(event.NodeID)
	if target == "" {
		return State{}, fmt.Errorf("intervention event: target node id is required")
	}
	reason := strings.TrimSpace(event.Detail)
	if reason == "" {
		return State{}, fmt.Errorf("intervention event for %s: reason is required", target)
	}
	out := cloneState(state)
	out.Interventions = append(out.Interventions, Intervention{
		Kind:         kind,
		Reason:       reason,
		TargetNodeID: target,
		Status:       InterventionStatus(status),
	})
	return out, nil
}

// cloneState deep-copies the mutable parts of state so Apply never mutates
// its input. Slice headers and maps are copied; node structs are replaced,
// never edited in place.
func cloneState(state State) State {
	out := state
	out.Nodes = append([]ExecutionNode(nil), state.Nodes...)
	for i := range out.Nodes {
		out.Nodes[i].Usage.ByNode = cloneTokenUsage(state.Nodes[i].Usage.ByNode)
	}
	out.Interventions = append([]Intervention(nil), state.Interventions...)
	out.Evidence = append([]EvidenceGroup(nil), state.Evidence...)
	out.Files = append([]FileChangeSummary(nil), state.Files...)
	out.Trajectory.PassScores = append([]float64(nil), state.Trajectory.PassScores...)
	out.Trajectory.RestoreMarkers = append([]string(nil), state.Trajectory.RestoreMarkers...)
	out.Usage.ByNode = cloneTokenUsage(state.Usage.ByNode)
	if state.Gate != nil {
		gate := *state.Gate
		out.Gate = &gate
	}
	if state.Completion != nil {
		completion := *state.Completion
		out.Completion = &completion
	}
	return out
}

func cloneTokenUsage(source map[string]TokenUsage) map[string]TokenUsage {
	if source == nil {
		return nil
	}
	out := make(map[string]TokenUsage, len(source))
	for nodeID, usage := range source {
		out[nodeID] = usage
	}
	return out
}
