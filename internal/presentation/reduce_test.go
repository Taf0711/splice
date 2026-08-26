package presentation

import (
	"strings"
	"testing"
)

func TestApplyStageEvents(t *testing.T) {
	t.Run("started appends node", func(t *testing.T) {
		state, err := Apply(State{}, StageEvent{ID: "code_writer", Kind: NodeKindWrite, Status: "running"})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(state.Nodes) != 1 {
			t.Fatalf("nodes = %d, want 1", len(state.Nodes))
		}
		node := state.Nodes[0]
		if node.ID != "code_writer" || node.Status != NodeStatusRunning || node.Progress != 0 || node.Kind != NodeKindWrite {
			t.Fatalf("unexpected node: %+v", node)
		}
		if node.Label != "code_writer" {
			t.Fatalf("label = %q, want node id", node.Label)
		}
		if err := state.Validate(); err != nil {
			t.Fatalf("reducer output invalid: %v", err)
		}
	})

	t.Run("progress updates existing node", func(t *testing.T) {
		state, _ := Apply(State{}, StageEvent{ID: "code_writer", Kind: NodeKindWrite, Status: "running"})
		state, err := Apply(state, StageEvent{ID: "code_writer", Kind: NodeKindWrite, Status: "running", Progress: 50})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(state.Nodes) != 1 {
			t.Fatalf("nodes = %d, want 1 (no duplicate)", len(state.Nodes))
		}
		if state.Nodes[0].Progress != 0.5 {
			t.Fatalf("progress = %v, want 0.5", state.Nodes[0].Progress)
		}
	})

	t.Run("completed and failed transitions", func(t *testing.T) {
		state, _ := Apply(State{}, StageEvent{ID: "code_writer", Kind: NodeKindWrite, Status: "running"})
		state, err := Apply(state, StageEvent{ID: "code_writer", Kind: NodeKindWrite, Status: "completed", Progress: 100})
		if err != nil {
			t.Fatalf("Apply completed: %v", err)
		}
		if state.Nodes[0].Status != NodeStatusComplete || state.Nodes[0].Progress != 1 {
			t.Fatalf("unexpected completed node: %+v", state.Nodes[0])
		}
		state, err = Apply(State{}, StageEvent{ID: "test_runner", Kind: NodeKindTest, Status: "failed"})
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		if state.Nodes[0].Status != NodeStatusFailed {
			t.Fatalf("unexpected failed node: %+v", state.Nodes[0])
		}
	})

	t.Run("repair re-entry regresses terminal status", func(t *testing.T) {
		state, _ := Apply(State{}, StageEvent{ID: "code_writer", Kind: NodeKindWrite, Status: "completed", Progress: 100})
		state, err := Apply(state, StageEvent{ID: "code_writer", Kind: NodeKindWrite, Status: "running", Progress: 10})
		if err != nil {
			t.Fatalf("Apply re-entry: %v", err)
		}
		if state.Nodes[0].Status != NodeStatusRunning {
			t.Fatalf("re-entry status = %q, want running", state.Nodes[0].Status)
		}
		if len(state.Nodes) != 1 {
			t.Fatalf("re-entry duplicated node: %d nodes", len(state.Nodes))
		}
	})

	t.Run("skipped maps to pending, incomplete to degraded", func(t *testing.T) {
		skipped, _ := Apply(State{}, StageEvent{ID: "lint", Kind: NodeKindLint, Status: "skipped"})
		if skipped.Nodes[0].Status != NodeStatusPending {
			t.Fatalf("skipped status = %q, want pending", skipped.Nodes[0].Status)
		}
		incomplete, _ := Apply(State{}, StageEvent{ID: "auditor", Kind: NodeKindSecurity, Status: "incomplete"})
		if incomplete.Nodes[0].Status != NodeStatusDegraded {
			t.Fatalf("incomplete status = %q, want degraded", incomplete.Nodes[0].Status)
		}
	})
}

func TestApplyErrors(t *testing.T) {
	cases := []struct {
		name  string
		state State
		event StreamEventLike
		want  string
	}{
		{"nil event", State{}, nil, "event is nil"},
		{"unknown kind", State{}, kindlessEvent{}, "unknown event kind"},
		{"unsupported schema version", State{SchemaVersion: 9}, StageEvent{ID: "a", Kind: NodeKindWrite, Status: "running"}, "schema_version"},
		{"stage without node id", State{}, StageEvent{Kind: NodeKindWrite, Status: "running"}, "node id is required"},
		{"stage unknown status", State{}, StageEvent{ID: "a", Kind: NodeKindWrite, Status: "message"}, "unknown stage status"},
		{"stage without kind", State{}, StageEvent{ID: "a", Status: "running"}, "node kind is required"},
		{"stage progress out of range", State{}, StageEvent{ID: "a", Kind: NodeKindWrite, Status: "running", Progress: 101}, "progress"},
		{"plan after completion", State{Lifecycle: LifecycleComplete}, PlanEvent{}, "plan event after completion"},
		{"run unknown status", State{Lifecycle: LifecycleExecute}, RunEvent{Status: "maybe"}, "unknown run status"},
		{"run without execute", State{}, RunEvent{Status: "completed"}, "lifecycle"},
		{"run duplicate completion", State{Lifecycle: LifecycleComplete}, RunEvent{Status: "completed"}, "lifecycle"},
		{"intervention unknown kind", State{}, InterventionEvent{Kind: "pause", NodeID: "a", Status: "proposed", Reason: "r"}, "unknown intervention kind"},
		{"intervention unknown status", State{}, InterventionEvent{Kind: InterventionRetry, NodeID: "a", Status: "maybe", Reason: "r"}, "unknown intervention status"},
		{"intervention without target", State{}, InterventionEvent{Kind: InterventionRetry, Status: "proposed", Reason: "r"}, "target node id is required"},
		{"intervention without reason", State{}, InterventionEvent{Kind: InterventionRetry, NodeID: "a", Status: "proposed"}, "reason is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Apply(tc.state, tc.event)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

// kindlessEvent is an event adapter whose normalized kind is unknown, proving
// the reducer rejects unknown kinds instead of ignoring them.
type kindlessEvent struct{}

func (kindlessEvent) PresentationEvent() Event { return Event{Kind: EventKind("bogus")} }

func TestApplyPlanAndRun(t *testing.T) {
	t.Run("plan sets lifecycle and roster count", func(t *testing.T) {
		state, err := Apply(State{}, PlanEvent{Title: "hello service", StageNames: []string{"code_writer", "test_runner"}})
		if err != nil {
			t.Fatalf("Apply plan: %v", err)
		}
		if state.Lifecycle != LifecycleExecute {
			t.Fatalf("lifecycle = %q, want execute", state.Lifecycle)
		}
		if state.Plan.TaskCount != 2 || state.Plan.Title != "hello service" {
			t.Fatalf("plan = %+v", state.Plan)
		}
	})

	t.Run("run completes from execute", func(t *testing.T) {
		state, _ := Apply(State{}, PlanEvent{StageNames: []string{"code_writer"}})
		state, err := Apply(state, RunEvent{Status: "completed", Detail: "all green"})
		if err != nil {
			t.Fatalf("Apply run: %v", err)
		}
		if state.Lifecycle != LifecycleComplete {
			t.Fatalf("lifecycle = %q, want complete", state.Lifecycle)
		}
		if state.Completion == nil || state.Completion.Status != "completed" || state.Completion.Detail != "all green" {
			t.Fatalf("completion = %+v", state.Completion)
		}
		if err := state.Validate(); err != nil {
			t.Fatalf("reducer output invalid: %v", err)
		}
	})

	t.Run("run failed from recovery", func(t *testing.T) {
		state, _ := Apply(State{}, PlanEvent{})
		state.Lifecycle = LifecycleRecovery
		state, err := Apply(state, RunEvent{Status: "failed"})
		if err != nil {
			t.Fatalf("Apply run: %v", err)
		}
		if state.Completion == nil || state.Completion.Status != "failed" {
			t.Fatalf("completion = %+v", state.Completion)
		}
	})
}

func TestApplyIntervention(t *testing.T) {
	state, err := Apply(State{}, InterventionEvent{Kind: InterventionRetry, NodeID: "test_runner", Status: "proposed", Reason: "suite failed"})
	if err != nil {
		t.Fatalf("Apply intervention: %v", err)
	}
	if len(state.Interventions) != 1 {
		t.Fatalf("interventions = %d, want 1", len(state.Interventions))
	}
	intervention := state.Interventions[0]
	if intervention.Kind != InterventionRetry || intervention.TargetNodeID != "test_runner" ||
		intervention.Status != InterventionProposed || intervention.Reason != "suite failed" {
		t.Fatalf("unexpected intervention: %+v", intervention)
	}
	state, err = Apply(state, InterventionEvent{Kind: InterventionRetry, NodeID: "test_runner", Status: "applied", Reason: "retry scheduled"})
	if err != nil {
		t.Fatalf("Apply applied intervention: %v", err)
	}
	if len(state.Interventions) != 2 || state.Interventions[1].Status != InterventionApplied {
		t.Fatalf("applied intervention not appended: %+v", state.Interventions)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("reducer output invalid: %v", err)
	}
}

func TestApplyPurity(t *testing.T) {
	state := State{SchemaVersion: PresentationSchemaVersionV1, Lifecycle: LifecycleExecute}
	_, err := Apply(state, StageEvent{ID: "code_writer", Kind: NodeKindWrite, Status: "running"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(state.Nodes) != 0 {
		t.Fatal("input state was mutated")
	}
	if state.Lifecycle != LifecycleExecute {
		t.Fatal("input state lifecycle was mutated")
	}
}

func TestApplyIdempotencyAndOrdering(t *testing.T) {
	t.Run("progress update is idempotent", func(t *testing.T) {
		state, _ := Apply(State{}, StageEvent{ID: "a", Kind: NodeKindWrite, Status: "running", Progress: 40})
		first, err := Apply(state, StageEvent{ID: "a", Kind: NodeKindWrite, Status: "running", Progress: 40})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		second, err := Apply(state, StageEvent{ID: "a", Kind: NodeKindWrite, Status: "running", Progress: 40})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if first.Nodes[0].Progress != second.Nodes[0].Progress {
			t.Fatal("same event produced different progress")
		}
	})

	t.Run("duplicate completion errors", func(t *testing.T) {
		state, _ := Apply(State{}, PlanEvent{})
		state, _ = Apply(state, RunEvent{Status: "completed"})
		if _, err := Apply(state, RunEvent{Status: "completed"}); err == nil {
			t.Fatal("duplicate completion accepted")
		}
	})

	t.Run("node ordering is stable", func(t *testing.T) {
		state := State{}
		for _, id := range []string{"third", "first", "second"} {
			state, _ = Apply(state, StageEvent{ID: id, Kind: NodeKindCustom, Status: "running"})
		}
		got := make([]string, 0, len(state.Nodes))
		for _, node := range state.Nodes {
			got = append(got, node.ID)
		}
		if strings.Join(got, ",") != "third,first,second" {
			t.Fatalf("append order not preserved: %v", got)
		}
	})
}

// TestPairingInvariant is the heart of the checkpoint: every state produced
// by a legal event sequence must pass Validate. Each sequence ends with
// Validate and must never fail it.
func TestPairingInvariant(t *testing.T) {
	sequences := []struct {
		name   string
		events []StreamEventLike
	}{
		{"stage only", []StreamEventLike{
			StageEvent{ID: "code_writer", Kind: NodeKindWrite, Status: "running"},
			StageEvent{ID: "code_writer", Kind: NodeKindWrite, Status: "completed", Progress: 100},
		}},
		{"full happy path", []StreamEventLike{
			PlanEvent{Title: "hello", StageNames: []string{"code_writer", "test_runner", "security_auditor"}},
			StageEvent{ID: "code_writer", Kind: NodeKindWrite, Status: "running"},
			StageEvent{ID: "code_writer", Kind: NodeKindWrite, Status: "completed", Progress: 100},
			StageEvent{ID: "test_runner", Kind: NodeKindTest, Status: "running", Progress: 50},
			StageEvent{ID: "test_runner", Kind: NodeKindTest, Status: "failed"},
			StageEvent{ID: "security_auditor", Kind: NodeKindSecurity, Status: "skipped"},
			RunEvent{Status: "failed", Detail: "test suite failed"},
		}},
		{"repair cycle with intervention", []StreamEventLike{
			PlanEvent{StageNames: []string{"code_writer"}},
			StageEvent{ID: "code_writer", Kind: NodeKindWrite, Status: "running"},
			StageEvent{ID: "code_writer", Kind: NodeKindWrite, Status: "completed", Progress: 100},
			StageEvent{ID: "test_runner", Kind: NodeKindTest, Status: "failed"},
			InterventionEvent{Kind: InterventionRetry, NodeID: "test_runner", Status: "proposed", Reason: "suite failed twice"},
			InterventionEvent{Kind: InterventionRetry, NodeID: "test_runner", Status: "applied", Reason: "retry scheduled"},
			StageEvent{ID: "test_runner", Kind: NodeKindTest, Status: "running", Progress: 10},
			StageEvent{ID: "test_runner", Kind: NodeKindTest, Status: "completed", Progress: 100},
			RunEvent{Status: "completed"},
		}},
		{"incomplete and degraded", []StreamEventLike{
			PlanEvent{},
			StageEvent{ID: "auditor", Kind: NodeKindSecurity, Status: "incomplete"},
			RunEvent{Status: "completed"},
		}},
		{"all eight node kinds", []StreamEventLike{
			PlanEvent{},
			StageEvent{ID: "w", Kind: NodeKindWrite, Status: "completed", Progress: 100},
			StageEvent{ID: "a", Kind: NodeKindAnalyze, Status: "completed", Progress: 100},
			StageEvent{ID: "s", Kind: NodeKindSecurity, Status: "completed", Progress: 100},
			StageEvent{ID: "l", Kind: NodeKindLint, Status: "completed", Progress: 100},
			StageEvent{ID: "c", Kind: NodeKindCustom, Status: "completed", Progress: 100},
			StageEvent{ID: "t", Kind: NodeKindTest, Status: "completed", Progress: 100},
			StageEvent{ID: "v", Kind: NodeKindVerify, Status: "completed", Progress: 100},
			StageEvent{ID: "r", Kind: NodeKindReview, Status: "completed", Progress: 100},
			RunEvent{Status: "completed"},
		}},
	}
	for _, tc := range sequences {
		t.Run(tc.name, func(t *testing.T) {
			state := State{}
			for i, event := range tc.events {
				next, err := Apply(state, event)
				if err != nil {
					t.Fatalf("event %d: %v", i, err)
				}
				state = next
			}
			if err := state.Validate(); err != nil {
				t.Fatalf("state from legal sequence failed Validate: %v\nstate: %+v", err, state)
			}
		})
	}
}
