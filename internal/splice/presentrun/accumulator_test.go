package presentrun

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
)

func TestAccumulatorAppliesAndSnapshots(t *testing.T) {
	acc := New(nil)
	acc.Apply(AdaptPlanEvent("hello", []string{"code_writer"}))
	acc.Apply(AdaptStageEvent(agent.StageEvent{Name: "code_writer", Status: "running"}))
	acc.Apply(AdaptStageEvent(agent.StageEvent{Name: "code_writer", Status: "completed"}))

	state := acc.Snapshot()
	if err := state.Validate(); err != nil {
		t.Fatalf("snapshot invalid: %v", err)
	}
	if len(state.Nodes) != 1 || state.Nodes[0].Status != presentation.NodeStatusComplete {
		t.Fatalf("unexpected nodes: %+v", state.Nodes)
	}
	applied, skipped, errors := acc.Counts()
	if applied != 3 || skipped != 0 || errors != 0 {
		t.Fatalf("counts = (%d, %d, %d), want (3, 0, 0)", applied, skipped, errors)
	}
}

func TestAccumulatorErrorPolicy(t *testing.T) {
	// Error policy: a malformed event is counted and logged, never fatal.
	// The snapshot stays at its last good value.
	var warnings []string
	acc := New(func(msg string) { warnings = append(warnings, msg) })
	acc.Apply(AdaptPlanEvent("hello", []string{"code_writer"}))
	acc.Apply(AdaptStageEvent(agent.StageEvent{Name: "code_writer", Status: "running"}))

	// Malformed event: an unknown stage status the reducer refuses.
	acc.Apply(presentation.StageEvent{ID: "code_writer", Kind: presentation.NodeKindWrite, Status: "bogus"})

	if got := acc.Snapshot(); got.Lifecycle != presentation.LifecycleExecute || len(got.Nodes) != 1 {
		t.Fatalf("last-good state lost after refused event: %+v", got)
	}
	if err := acc.Snapshot().Validate(); err != nil {
		t.Fatalf("snapshot invalid after refused event: %v", err)
	}
	applied, skipped, errors := acc.Counts()
	if applied != 2 || skipped != 0 || errors != 1 {
		t.Fatalf("counts = (%d, %d, %d), want (2, 0, 1)", applied, skipped, errors)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "ignoring presentation event") {
		t.Fatalf("warnings = %v, want one refusal log", warnings)
	}
}

func TestAccumulatorSkipCounts(t *testing.T) {
	acc := New(nil)
	acc.Skip()
	acc.Skip()
	applied, skipped, errors := acc.Counts()
	if applied != 0 || skipped != 2 || errors != 0 {
		t.Fatalf("counts = (%d, %d, %d), want (0, 2, 0)", applied, skipped, errors)
	}
}
