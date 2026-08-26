package presentrun

import (
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestNodeKindForStage(t *testing.T) {
	cases := map[string]presentation.NodeKind{
		"code_writer":         presentation.NodeKindWrite,
		"test_generator":      presentation.NodeKindWrite,
		"static_analyzer":     presentation.NodeKindAnalyze,
		"security_auditor":    presentation.NodeKindSecurity,
		"test_runner":         presentation.NodeKindTest,
		"acceptance_verifier": presentation.NodeKindVerify,
	}
	for name, want := range cases {
		if got := NodeKindForStage(name); got != want {
			t.Fatalf("NodeKindForStage(%q) = %q, want %q", name, got, want)
		}
	}
	if got := NodeKindForStage("future_stage"); got != presentation.NodeKindCustom {
		t.Fatalf("NodeKindForStage(unknown) = %q, want CUSTOM", got)
	}
}

func TestAdaptStageEvent(t *testing.T) {
	t.Run("stage transition", func(t *testing.T) {
		adapted := AdaptStageEvent(agent.StageEvent{Name: "code_writer", Status: "running", Detail: "writing", Progress: 50})
		event := adapted.PresentationEvent()
		if event.Kind != presentation.EventKindStage || event.NodeID != "code_writer" ||
			event.NodeKind != presentation.NodeKindWrite || event.Status != "running" ||
			event.Detail != "writing" || event.Progress != 50 {
			t.Fatalf("unexpected stage projection: %+v", event)
		}
	})

	t.Run("repair message becomes proposed rollback", func(t *testing.T) {
		adapted := AdaptStageEvent(agent.StageEvent{Name: "test_runner", Status: "message", Detail: "revision_request -> code_writer: 2 failing tests"})
		event := adapted.PresentationEvent()
		if event.Kind != presentation.EventKindIntervention || event.InterventionKind != presentation.InterventionRollback ||
			event.Status != string(presentation.InterventionProposed) || event.NodeID != "test_runner" {
			t.Fatalf("unexpected message projection: %+v", event)
		}
	})

	t.Run("repair resolved becomes applied continue", func(t *testing.T) {
		adapted := AdaptStageEvent(agent.StageEvent{Name: "test_runner", Status: "repaired", Detail: "revision resolved: tests pass"})
		event := adapted.PresentationEvent()
		if event.Kind != presentation.EventKindIntervention || event.InterventionKind != presentation.InterventionContinue ||
			event.Status != string(presentation.InterventionApplied) {
			t.Fatalf("unexpected repaired projection: %+v", event)
		}
	})
}

func TestAdaptPlanEvent(t *testing.T) {
	adapted := AdaptPlanEvent("hello service", []string{"code_writer", "test_runner"})
	event := adapted.PresentationEvent()
	if event.Kind != presentation.EventKindPlan || event.Title != "hello service" ||
		len(event.StageNames) != 2 || event.StageNames[1] != "test_runner" {
		t.Fatalf("unexpected plan projection: %+v", event)
	}
}

func TestAdaptTrajectoryDecision(t *testing.T) {
	t.Run("rollback is an intervention", func(t *testing.T) {
		adapted, ok := AdaptTrajectoryDecision(schemas.ActionRollback, "score regressed", "code_writer")
		if !ok {
			t.Fatal("rollback not adapted")
		}
		event := adapted.PresentationEvent()
		if event.InterventionKind != presentation.InterventionRollback || event.Status != string(presentation.InterventionApplied) ||
			event.NodeID != "code_writer" || event.Detail != "score regressed" {
			t.Fatalf("unexpected rollback projection: %+v", event)
		}
	})

	t.Run("step back and escalate map", func(t *testing.T) {
		for action, want := range map[schemas.TrajectoryAction]presentation.InterventionKind{
			schemas.ActionStepBack:              presentation.InterventionStepBack,
			schemas.ActionEscalateCycleDetected: presentation.InterventionEscalate,
			schemas.ActionEscalateOscillation:   presentation.InterventionEscalate,
			schemas.ActionSurfaceToUser:         presentation.InterventionAskUser,
		} {
			adapted, ok := AdaptTrajectoryDecision(action, "reason", "test_runner")
			if !ok {
				t.Fatalf("%s not adapted", action)
			}
			if adapted.PresentationEvent().InterventionKind != want {
				t.Fatalf("%s kind = %q, want %q", action, adapted.PresentationEvent().InterventionKind, want)
			}
		}
	})

	t.Run("continue and abort are not interventions", func(t *testing.T) {
		if IsTrajectoryIntervention(schemas.ActionContinue) {
			t.Fatal("continue is not an intervention")
		}
		if IsTrajectoryIntervention(schemas.ActionAbortHardLimit) {
			t.Fatal("abort is not an intervention")
		}
	})

	t.Run("no target is not representable", func(t *testing.T) {
		if _, ok := AdaptTrajectoryDecision(schemas.ActionRollback, "reason", ""); ok {
			t.Fatal("targetless rollback should not adapt")
		}
	})
}
