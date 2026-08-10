package splice

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func planCrystallizedEvent(t *testing.T, planID string, revision int, plan schemas.DesignPlan) sessions.Event {
	return sessions.Event{
		Type: sessions.EventPlanCrystallized,
		Payload: mustMarshal(t, struct {
			PlanID   string `json:"plan_id"`
			Revision int    `json:"revision"`
			Plan     any    `json:"plan"`
		}{planID, revision, plan}),
	}
}

func critiqueRecordedEvent(t *testing.T, planID string, revision int, critique schemas.PlanCritique) sessions.Event {
	return sessions.Event{
		Type: sessions.EventCritiqueRecorded,
		Payload: mustMarshal(t, struct {
			PlanID   string `json:"plan_id"`
			Revision int    `json:"revision"`
			Critique any    `json:"critique"`
		}{planID, revision, critique}),
	}
}

func TestAssembleDesignContext_IncludesHistoryPlanCritique(t *testing.T) {
	plan := validDesignPlan("epic")
	critique := validCritique("assessment")
	events := []sessions.Event{
		designModeEnteredEvent(),
		messageEvent("user", "hello"),
		messageEvent("assistant", "hi"),
		planCrystallizedEvent(t, "plan-1", 1, plan),
		critiqueRecordedEvent(t, "plan-1", 1, critique),
	}
	ctx, err := AssembleDesignContext(events, nil, nil)
	if err != nil {
		t.Fatalf("AssembleDesignContext: %v", err)
	}
	if len(ctx.History) != 2 {
		t.Fatalf("history = %d messages, want 2", len(ctx.History))
	}
	if ctx.CurrentPlan == nil || ctx.CurrentPlan.Epic != plan.Epic {
		t.Fatalf("current plan = %#v, want epic %q", ctx.CurrentPlan, plan.Epic)
	}
	if ctx.CurrentCritique == nil || ctx.CurrentCritique.OverallAssessment != critique.OverallAssessment {
		t.Fatalf("current critique = %#v, want assessment %q", ctx.CurrentCritique, critique.OverallAssessment)
	}
}

func TestAssembleDesignContext_LiveOverlayWinsOverPersisted(t *testing.T) {
	persistedPlan := validDesignPlan("persisted")
	livePlan := validDesignPlan("live")
	liveCritique := validCritique("live critique")
	events := []sessions.Event{
		designModeEnteredEvent(),
		messageEvent("user", "hello"),
		planCrystallizedEvent(t, "plan-1", 1, persistedPlan),
	}
	ctx, err := AssembleDesignContext(events, &livePlan, &liveCritique)
	if err != nil {
		t.Fatalf("AssembleDesignContext: %v", err)
	}
	if ctx.CurrentPlan == nil || ctx.CurrentPlan.Epic != "live" {
		t.Fatalf("live plan overlay did not win: %#v", ctx.CurrentPlan)
	}
	if ctx.CurrentCritique == nil || ctx.CurrentCritique.OverallAssessment != "live critique" {
		t.Fatalf("live critique overlay did not win: %#v", ctx.CurrentCritique)
	}
}

func TestAssembleDesignContext_LiveOverlayCoversPersistenceFailure(t *testing.T) {
	// A critique_recorded write failed, so events carry no critique. The live
	// overlay must supply it so a re-crystallization still sees the work the
	// user is revising.
	plan := validDesignPlan("epic")
	liveCritique := schemas.PlanCritique{
		OverallAssessment:      "needs work",
		Critiques:              []schemas.Critique{{Category: "correctness", Severity: schemas.SeverityHigh, Issue: "unsafe", SuggestedMitigation: "add checks"}},
		MustFixBeforeExecution: true,
	}
	events := []sessions.Event{
		designModeEnteredEvent(),
		messageEvent("user", "hello"),
		planCrystallizedEvent(t, "plan-1", 1, plan),
	}
	ctx, err := AssembleDesignContext(events, nil, &liveCritique)
	if err != nil {
		t.Fatalf("AssembleDesignContext: %v", err)
	}
	if ctx.CurrentCritique == nil || ctx.CurrentCritique.OverallAssessment != "needs work" {
		t.Fatalf("live critique did not cover persistence failure: %#v", ctx.CurrentCritique)
	}
}

func TestAssembleDesignContext_EpochResetExcludesEarlierPlanCritique(t *testing.T) {
	plan := validDesignPlan("old epic")
	critique := validCritique("old assessment")
	events := []sessions.Event{
		designModeEnteredEvent(),
		messageEvent("user", "old work"),
		planCrystallizedEvent(t, "plan-1", 1, plan),
		critiqueRecordedEvent(t, "plan-1", 1, critique),
		designModeEnteredEvent(),
		messageEvent("user", "new epoch"),
	}
	ctx, err := AssembleDesignContext(events, nil, nil)
	if err != nil {
		t.Fatalf("AssembleDesignContext: %v", err)
	}
	if len(ctx.History) != 1 || ctx.History[0].Content != "new epoch" {
		t.Fatalf("history did not reset on new epoch: %#v", ctx.History)
	}
	if ctx.CurrentPlan != nil || ctx.CurrentCritique != nil {
		t.Fatalf("earlier epoch plan/critique leaked into new epoch: plan=%#v critique=%#v", ctx.CurrentPlan, ctx.CurrentCritique)
	}
}

func TestAssembleDesignContext_MalformedLifecycleReturnsNamedError(t *testing.T) {
	events := []sessions.Event{
		designModeEnteredEvent(),
		// plan_crystallized with a plan_id but no plan payload.
		{Type: sessions.EventPlanCrystallized, Payload: []byte(`{"plan_id":"plan-1"}`)},
	}
	_, err := AssembleDesignContext(events, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "assemble design context") {
		t.Fatalf("expected named assembler error, got: %v", err)
	}
}
