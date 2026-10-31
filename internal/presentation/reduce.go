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
	EventKindUsage        EventKind = "usage"
	EventKindRoute        EventKind = "route"
)

// Event is the normalized event the reducer consumes. Its fields are chosen
// from the data the pipeline emits today, so a thin adapter can fill it from
// the runtime event types without modifying them. An unknown EventKind is an
// error, never a silent no-op.
type Event struct {
	Kind   EventKind
	NodeID string
	// Iteration is the pipeline pass the node event belongs to. Node
	// identity is (NodeID, Iteration), so a later pass does not overwrite an
	// earlier one.
	Iteration  int
	NodeKind   NodeKind
	Status     string
	Detail     string
	Progress   int
	StageNames []string
	// StageDependencies carries the compiled graph when the plan event has
	// one: stage name to the stages that run before it.
	StageDependencies map[string][]string
	// PlanTier and PlanTopology identify the compiled plan (plan events).
	PlanTier     string
	PlanTopology string
	PlanRemoved  map[string]string
	// Usage and CostUSD carry one request's spend (usage events). A nil
	// CostUSD means the request had no price.
	Usage   TokenUsage
	CostUSD *float64
	// Route is a node's applied route (route events).
	Route            NodeRoute
	Title            string
	InterventionKind InterventionKind
	// Unverified marks a run event whose completed run verified nothing.
	Unverified bool
	// Workspace isolation state (DoD 26), stamped by the adapter.
	Workspace    string
	WorktreePath string
	// ChangedFiles lists the files a completed stage reported changed. Other
	// stage events carry none.
	ChangedFiles []string
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
// the adapter), status, detail, and integer progress, plus the stage's
// workspace isolation state (DoD 26).
type StageEvent struct {
	ID string
	// Iteration is the pipeline pass this stage event belongs to. Together
	// with ID it is the node identity, so a re-entry pass keeps its own node.
	Iteration int
	Kind      NodeKind
	Status    string
	Detail    string
	Progress  int
	// Workspace is the stage's isolation state ("isolated"/"shared_cwd"),
	// stamped by the runtime; empty means unset.
	Workspace    string
	WorktreePath string
	// ChangedFiles lists the files a completed stage reported changed.
	ChangedFiles []string
}

// PresentationEvent projects the stage event into the normalized form.
func (e StageEvent) PresentationEvent() Event {
	return Event{
		Kind:      EventKindStage,
		NodeID:    e.ID,
		Iteration: e.Iteration,
		NodeKind:  e.Kind,
		Status:    e.Status,
		Detail:    e.Detail,
		Progress:  e.Progress,
		// Workspace flows through for DoD 26's isolation badge.
		Workspace:    e.Workspace,
		ChangedFiles: e.ChangedFiles,
		WorktreePath: e.WorktreePath,
	}
}

// PlanEvent announces the ordered stage roster of one pipeline plan.
type PlanEvent struct {
	Title        string
	StageNames   []string
	Dependencies map[string][]string
	// Tier and Topology identify the compiled plan. Both may be empty.
	Tier     string
	Topology string
	// Removed maps a removed stage to the reason.
	Removed map[string]string
}

// PresentationEvent projects the plan event into the normalized form.
func (e PlanEvent) PresentationEvent() Event {
	return Event{
		Kind:              EventKindPlan,
		StageNames:        e.StageNames,
		StageDependencies: clonePlanDependencies(e.Dependencies),
		Title:             e.Title,
		PlanTier:          e.Tier,
		PlanTopology:      e.Topology,
		PlanRemoved:       cloneStrings(e.Removed),
	}
}

// UsageEvent is the spend of one provider request made by a node.
type UsageEvent struct {
	NodeID    string
	Iteration int
	Usage     TokenUsage
	// CostUSD is nil when the request had no price.
	CostUSD *float64
}

// RouteEvent records the route a node pass applied when it started.
type RouteEvent struct {
	NodeID    string
	Iteration int
	Route     NodeRoute
}

// PresentationEvent projects the route event into the normalized form.
func (e RouteEvent) PresentationEvent() Event {
	return Event{Kind: EventKindRoute, NodeID: e.NodeID, Iteration: e.Iteration, Route: e.Route}
}

// PresentationEvent projects the usage event into the normalized form.
func (e UsageEvent) PresentationEvent() Event {
	return Event{Kind: EventKindUsage, NodeID: e.NodeID, Iteration: e.Iteration, Usage: e.Usage, CostUSD: e.CostUSD}
}

// RunEvent marks the end of a run.
type RunEvent struct {
	Status string // "completed" or "failed"
	Detail string
	// Unverified marks a completed run that verified nothing. Detail names why.
	Unverified bool
}

// PresentationEvent projects the run event into the normalized form.
func (e RunEvent) PresentationEvent() Event {
	return Event{Kind: EventKindRun, Status: e.Status, Detail: e.Detail, Unverified: e.Unverified}
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
	case EventKindUsage:
		out, err = applyUsage(state, normalized)
	case EventKindRoute:
		out, err = applyRoute(state, normalized)
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
		return NodeStatusSkipped
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
		return State{}, fmt.Errorf("stage event for %s iteration %d: %w", nodeID, event.Iteration, err)
	}
	out := cloneState(state)
	idx := -1
	for i := range out.Nodes {
		if out.Nodes[i].ID == nodeID && out.Nodes[i].Iteration == event.Iteration {
			idx = i
			break
		}
	}
	node := ExecutionNode{
		ID:        nodeID,
		Label:     nodeID,
		Iteration: event.Iteration,
		Kind:      kind,
		Status:    status,
		Progress:  float64(progress) / 100,
		// Workspace isolation flows from the runtime event (DoD 26).
		Workspace:    event.Workspace,
		WorktreePath: event.WorktreePath,
		// Each pass's node carries the files its latest stage event reported.
		ChangedFiles: append([]string(nil), event.ChangedFiles...),
	}
	// The plan graph is the source of a node's dependencies. It arrives with
	// the plan event, before any stage event, so it is available here.
	if deps := out.Plan.Dependencies[nodeID]; len(deps) > 0 {
		node.Dependencies = append([]string(nil), deps...)
	}
	if idx >= 0 {
		// Preserve per-node identity across updates. A terminal status may
		// regress to running: that is a repair re-entry, which is legal.
		prior := out.Nodes[idx]
		node.Iteration = prior.Iteration
		node.Cost = prior.Cost
		node.Usage = prior.Usage
		node.Summary = prior.Summary
		node.Route = prior.Route
		// The compiled graph is the source of truth; a node created without a
		// plan event keeps whatever an earlier event carried.
		if len(node.Dependencies) == 0 {
			node.Dependencies = prior.Dependencies
		}
		// Workspace identity persists across re-entries: a lane does not
		// change isolation mid-run.
		if node.Workspace == "" {
			node.Workspace = prior.Workspace
			node.WorktreePath = prior.WorktreePath
		}
		out.Nodes[idx] = node
	} else {
		out.Nodes = append(out.Nodes, node)
		idx = len(out.Nodes) - 1
	}
	// A terminal event's detail is the stage summary. A running event's
	// detail is only the activity, so the last summary stays.
	if status != NodeStatusRunning && strings.TrimSpace(event.Detail) != "" {
		out.Nodes[idx].Summary = event.Detail
	}
	// Health is projection too: a failed stage is failed health; a repair
	// re-entry of the same stage clears it back to normal (the intervention
	// path owns recovering). Regression/refining semantics stay with the
	// trajectory layer, which sets them through interventions.
	if status == NodeStatusFailed {
		if out.Health.Effective() != HealthRecovering {
			out.Health = HealthFailed
		}
	} else if (out.Health == HealthFailed || out.Health == HealthRegression) && status == NodeStatusRunning {
		// A failed stage re-entered, or a stage ran after a trajectory
		// regression: the intervention is in effect and the run is recovering.
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
	out.Plan = Plan{
		Title:        event.Title,
		TaskCount:    len(event.StageNames),
		Dependencies: clonePlanDependencies(event.StageDependencies),
		Stages:       append([]string(nil), event.StageNames...),
		Tier:         event.PlanTier,
		Topology:     event.PlanTopology,
		Removed:      cloneStrings(event.PlanRemoved),
	}
	return out, nil
}

// applyRoute stores the route a node pass applied. The pass must exist: the
// runtime emits the route after the stage's running event.
func applyRoute(state State, event Event) (State, error) {
	nodeID := strings.TrimSpace(event.NodeID)
	if nodeID == "" {
		return State{}, fmt.Errorf("route event: node id is required")
	}
	if err := event.Route.Validate(); err != nil {
		return State{}, fmt.Errorf("route event for %s: %w", nodeID, err)
	}
	for i, node := range state.Nodes {
		if node.ID == nodeID && node.Iteration == event.Iteration {
			out := cloneState(state)
			route := event.Route
			out.Nodes[i].Route = &route
			return out, nil
		}
	}
	return State{}, fmt.Errorf("route event for unknown node %s iteration %d", nodeID, event.Iteration)
}

// applyUsage adds one request's spend to its node. The (node, iteration)
// match wins. A request of a repair re-entry that has no node of its own
// counts on the latest pass of the same node. A request for a node that
// does not exist is an error: the caller forwards only plan stages.
func applyUsage(state State, event Event) (State, error) {
	nodeID := strings.TrimSpace(event.NodeID)
	if nodeID == "" {
		return State{}, fmt.Errorf("usage event: node id is required")
	}
	if err := event.Usage.Validate(); err != nil {
		return State{}, fmt.Errorf("usage event for %s: %w", nodeID, err)
	}
	if event.CostUSD != nil {
		if err := (CostSummary{USD: *event.CostUSD}).Validate(); err != nil {
			return State{}, fmt.Errorf("usage event for %s: %w", nodeID, err)
		}
	}
	idx := -1
	for i, node := range state.Nodes {
		if node.ID != nodeID {
			continue
		}
		if node.Iteration == event.Iteration {
			idx = i
			break
		}
		if idx < 0 || node.Iteration > state.Nodes[idx].Iteration {
			idx = i
		}
	}
	if idx < 0 {
		return State{}, fmt.Errorf("usage event for unknown node %s iteration %d", nodeID, event.Iteration)
	}
	out := cloneState(state)
	node := &out.Nodes[idx]
	node.Usage.InputTokens += event.Usage.InputTokens
	node.Usage.OutputTokens += event.Usage.OutputTokens
	node.Usage.CachedTokens += event.Usage.CachedTokens
	node.Usage.ReasoningTokens += event.Usage.ReasoningTokens
	node.Cost.Tokens += event.Usage.InputTokens + event.Usage.OutputTokens
	if event.CostUSD != nil {
		node.Cost.USD += *event.CostUSD
	} else {
		node.Cost.Unpriced = true
	}
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
	out.Completion = &CompletionReceipt{Status: status, Detail: event.Detail, Unverified: status == "completed" && event.Unverified}
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
	// An APPLIED corrective intervention is the trajectory layer saying the
	// evidence regressed; a proposal is only a suggestion and changes
	// nothing. Terminal health is final.
	if status == string(InterventionApplied) && out.Health != HealthFailed && out.Health != HealthCancelled {
		switch kind {
		case InterventionRollback, InterventionStepBack, InterventionEscalate:
			out.Health = HealthRegression
		case InterventionContinue:
			if out.Health == HealthRegression {
				out.Health = HealthRecovering
			}
		}
	}
	return out, nil
}

// cloneState deep-copies the mutable parts of state so Apply never mutates
// its input. Slice headers and maps are copied; node structs are replaced,
// never edited in place.
func cloneState(state State) State {
	out := state
	out.Nodes = append([]ExecutionNode(nil), state.Nodes...)
	out.Plan.Dependencies = clonePlanDependencies(state.Plan.Dependencies)
	out.Plan.Stages = append([]string(nil), state.Plan.Stages...)
	out.Plan.Removed = cloneStrings(state.Plan.Removed)
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

func cloneStrings(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func clonePlanDependencies(source map[string][]string) map[string][]string {
	if source == nil {
		return nil
	}
	out := make(map[string][]string, len(source))
	for name, deps := range source {
		out[name] = append([]string(nil), deps...)
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
