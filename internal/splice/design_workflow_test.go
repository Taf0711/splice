package splice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type fakeWorkflowProvider struct {
	request zeroruntime.CompletionRequest
	events  []zeroruntime.StreamEvent
}

func (f *fakeWorkflowProvider) StreamCompletion(ctx context.Context, req zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	f.request = req
	ch := make(chan zeroruntime.StreamEvent, len(f.events))
	for _, e := range f.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func workflowToolCall(id, name, args string) []zeroruntime.StreamEvent {
	return []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: id, ToolName: name},
		{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: id, ArgumentsFragment: args},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: id},
	}
}

func workflowDone() []zeroruntime.StreamEvent {
	return []zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventDone}}
}

func validDesignPlan(epic string) schemas.DesignPlan {
	return schemas.DesignPlan{
		Epic:         epic,
		Requirements: []string{"the system must work"},
		InScope:      []string{"core flow"},
		OutOfScope:   []string{"enterprise features"},
		SystemDesign: "use go structs",
		Tasks: []schemas.Task{
			{ID: "t1", Title: "build it", Intent: "implement the core flow"},
		},
	}
}

func validCritique(assessment string) schemas.PlanCritique {
	return schemas.PlanCritique{
		OverallAssessment:      assessment,
		MustFixBeforeExecution: false,
	}
}

func TestCrystallizeAndCritique_Success(t *testing.T) {
	ctx := context.Background()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	if _, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	events := []sessions.Event{
		{Type: sessions.EventDesignModeEntered, Payload: nil},
		{Type: sessions.EventMessage, Payload: mustMarshal(t, map[string]string{"role": "user", "content": "build a thing"})},
	}

	plan := validDesignPlan("build a thing")
	planArgs, _ := json.Marshal(plan)
	critique := validCritique("looks good")
	critiqueArgs, _ := json.Marshal(critique)

	provider := &fakeWorkflowProvider{events: concatEvents(
		workflowToolCall("call-plan", "submit_design_plan", string(planArgs)),
		workflowToolCall("call-critique", "submit_critique", string(critiqueArgs)),
		workflowDone(),
	)}

	wf := NewDesignWorkflow(store, "test-session", "plan-1")
	gotPlan, gotCritique, err := wf.CrystallizeAndCritique(ctx, events, provider, nil, zeroruntime.CollectOptions{}, "", nil)
	if err != nil {
		t.Fatalf("CrystallizeAndCritique: %v", err)
	}
	if gotPlan.Epic != plan.Epic {
		t.Errorf("plan epic = %q, want %q", gotPlan.Epic, plan.Epic)
	}
	if gotCritique.OverallAssessment != critique.OverallAssessment {
		t.Errorf("critique assessment = %q, want %q", gotCritique.OverallAssessment, critique.OverallAssessment)
	}

	saved, err := store.ReadEvents("test-session")
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(saved) != 2 {
		t.Fatalf("expected 2 persisted events, got %d", len(saved))
	}
	if saved[0].Type != sessions.EventPlanCrystallized {
		t.Errorf("event[0] type = %q, want plan_crystallized", saved[0].Type)
	}
	if saved[1].Type != sessions.EventCritiqueRecorded {
		t.Errorf("event[1] type = %q, want critique_recorded", saved[1].Type)
	}
}

func TestCrystallizeAndCritique_EmptyHistory(t *testing.T) {
	ctx := context.Background()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	if _, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	events := []sessions.Event{
		{Type: sessions.EventDesignModeEntered, Payload: nil},
	}

	wf := NewDesignWorkflow(store, "test-session", "plan-1")
	_, _, err := wf.CrystallizeAndCritique(ctx, events, &fakeWorkflowProvider{}, nil, zeroruntime.CollectOptions{}, "", nil)
	if err == nil || err.Error() != "crystallize requires at least one conversation message" {
		t.Fatalf("expected empty history error, got: %v", err)
	}
}

func TestCrystallizeAndCritique_NilResolverUsesDefaultProvider(t *testing.T) {
	ctx := context.Background()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	if _, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	events := []sessions.Event{
		{Type: sessions.EventDesignModeEntered, Payload: nil},
		{Type: sessions.EventMessage, Payload: mustMarshal(t, map[string]string{"role": "user", "content": "do it"})},
	}

	plan := validDesignPlan("do it")
	planArgs, _ := json.Marshal(plan)
	critique := validCritique("fine")
	critiqueArgs, _ := json.Marshal(critique)
	provider := &fakeWorkflowProvider{events: concatEvents(
		workflowToolCall("call-plan", "submit_design_plan", string(planArgs)),
		workflowToolCall("call-critique", "submit_critique", string(critiqueArgs)),
		workflowDone(),
	)}

	wf := NewDesignWorkflow(store, "test-session", "plan-1")
	if _, _, err := wf.CrystallizeAndCritique(ctx, events, provider, nil, zeroruntime.CollectOptions{}, "", nil); err != nil {
		t.Fatalf("CrystallizeAndCritique: %v", err)
	}

	if len(provider.request.Messages) == 0 {
		t.Fatalf("default provider was not used")
	}
}

func TestCrystallizeAndCritique_ResolverChoosesProviderPerStage(t *testing.T) {
	ctx := context.Background()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	if _, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	events := []sessions.Event{
		{Type: sessions.EventDesignModeEntered, Payload: nil},
		{Type: sessions.EventMessage, Payload: mustMarshal(t, map[string]string{"role": "user", "content": "do it"})},
	}

	plan := validDesignPlan("do it")
	planArgs, _ := json.Marshal(plan)
	critique := validCritique("fine")
	critiqueArgs, _ := json.Marshal(critique)

	crystallizeProvider := &fakeWorkflowProvider{events: concatEvents(
		workflowToolCall("call-plan", "submit_design_plan", string(planArgs)),
		workflowDone(),
	)}
	criticProvider := &fakeWorkflowProvider{events: concatEvents(
		workflowToolCall("call-critique", "submit_critique", string(critiqueArgs)),
		workflowDone(),
	)}

	resolver := func(stage string) (agent.Provider, string, string, error) {
		switch stage {
		case "design_crystallize":
			return crystallizeProvider, "model-a", "high", nil
		case "plan_critic":
			return criticProvider, "model-b", "low", nil
		default:
			return nil, "", "", fmt.Errorf("unknown stage %q", stage)
		}
	}

	wf := NewDesignWorkflow(store, "test-session", "plan-1")
	if _, _, err := wf.CrystallizeAndCritique(ctx, events, &fakeWorkflowProvider{}, resolver, zeroruntime.CollectOptions{}, "", nil); err != nil {
		t.Fatalf("CrystallizeAndCritique: %v", err)
	}

	if len(crystallizeProvider.request.Messages) == 0 {
		t.Errorf("crystallize provider was not called")
	}
	if len(criticProvider.request.Messages) == 0 {
		t.Errorf("critic provider was not called")
	}
}

func TestCrystallizeAndCritique_RevisionIncrements(t *testing.T) {
	ctx := context.Background()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	if _, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if _, err := store.AppendEvent("test-session", sessions.AppendEventInput{
		Type:    sessions.EventDesignModeEntered,
		Payload: nil,
	}); err != nil {
		t.Fatalf("append design_mode_entered: %v", err)
	}
	if _, err := store.AppendEvent("test-session", sessions.AppendEventInput{
		Type:    sessions.EventMessage,
		Payload: map[string]string{"role": "user", "content": "do it"},
	}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	events, err := store.ReadEvents("test-session")
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}

	plan := validDesignPlan("do it")
	planArgs, _ := json.Marshal(plan)
	critique := validCritique("fine")
	critiqueArgs, _ := json.Marshal(critique)

	provider := &fakeWorkflowProvider{events: concatEvents(
		workflowToolCall("call-plan", "submit_design_plan", string(planArgs)),
		workflowToolCall("call-critique", "submit_critique", string(critiqueArgs)),
		workflowDone(),
	)}

	wf := NewDesignWorkflow(store, "test-session", "plan-1")
	if _, _, err := wf.CrystallizeAndCritique(ctx, events, provider, nil, zeroruntime.CollectOptions{}, "", nil); err != nil {
		t.Fatalf("first CrystallizeAndCritique: %v", err)
	}

	events, err = store.ReadEvents("test-session")
	if err != nil {
		t.Fatalf("reload events: %v", err)
	}

	plan2 := validDesignPlan("do it again")
	plan2Args, _ := json.Marshal(plan2)
	critique2 := validCritique("still fine")
	critique2Args, _ := json.Marshal(critique2)
	provider2 := &fakeWorkflowProvider{events: concatEvents(
		workflowToolCall("call-plan", "submit_design_plan", string(plan2Args)),
		workflowToolCall("call-critique", "submit_critique", string(critique2Args)),
		workflowDone(),
	)}
	if _, _, err := wf.CrystallizeAndCritique(ctx, events, provider2, nil, zeroruntime.CollectOptions{}, "", nil); err != nil {
		t.Fatalf("second CrystallizeAndCritique: %v", err)
	}

	saved, err := store.ReadEvents("test-session")
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}

	var revisions []int
	for _, ev := range saved {
		if ev.Type == sessions.EventPlanCrystallized {
			var p PlanCrystallizedPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatalf("unmarshal plan payload: %v", err)
			}
			revisions = append(revisions, p.Revision)
		}
	}
	if len(revisions) != 2 || revisions[0] != 1 || revisions[1] != 2 {
		t.Fatalf("expected revisions [1, 2], got %v", revisions)
	}
}

func TestCrystallizeAndCritique_CriticErrorReturnsPlan(t *testing.T) {
	ctx := context.Background()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	if _, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	events := []sessions.Event{
		{Type: sessions.EventDesignModeEntered, Payload: nil},
		{Type: sessions.EventMessage, Payload: mustMarshal(t, map[string]string{"role": "user", "content": "do it"})},
	}

	plan := validDesignPlan("do it")
	planArgs, _ := json.Marshal(plan)
	// No submit_critique tool call; critic stage will fail.
	provider := &fakeWorkflowProvider{events: concatEvents(
		workflowToolCall("call-plan", "submit_design_plan", string(planArgs)),
		workflowDone(),
	)}

	wf := NewDesignWorkflow(store, "test-session", "plan-1")
	gotPlan, gotCritique, err := wf.CrystallizeAndCritique(ctx, events, provider, nil, zeroruntime.CollectOptions{}, "", nil)
	if err == nil {
		t.Fatalf("expected critic error")
	}
	if gotPlan.Epic != plan.Epic {
		t.Errorf("returned plan epic = %q, want %q", gotPlan.Epic, plan.Epic)
	}
	if gotCritique.Critiques != nil || gotCritique.OverallAssessment != "" {
		t.Errorf("expected zero-value critique, got %#v", gotCritique)
	}

	saved, err := store.ReadEvents("test-session")
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	var found bool
	for _, ev := range saved {
		if ev.Type == sessions.EventPlanCrystallized {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("plan_crystallized event not persisted after critic error")
	}
}

func TestCrystallizeAndCritique_ResolverErrorFallsBack(t *testing.T) {
	ctx := context.Background()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	if _, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	events := []sessions.Event{
		{Type: sessions.EventDesignModeEntered, Payload: nil},
		{Type: sessions.EventMessage, Payload: mustMarshal(t, map[string]string{"role": "user", "content": "do it"})},
	}

	plan := validDesignPlan("do it")
	planArgs, _ := json.Marshal(plan)
	critique := validCritique("fine")
	critiqueArgs, _ := json.Marshal(critique)

	defaultProvider := &fakeWorkflowProvider{events: concatEvents(
		workflowToolCall("call-plan", "submit_design_plan", string(planArgs)),
		workflowToolCall("call-critique", "submit_critique", string(critiqueArgs)),
		workflowDone(),
	)}

	resolver := func(stage string) (agent.Provider, string, string, error) {
		return nil, "", "", errors.New("resolver unavailable")
	}

	wf := NewDesignWorkflow(store, "test-session", "plan-1")
	if _, _, err := wf.CrystallizeAndCritique(ctx, events, defaultProvider, resolver, zeroruntime.CollectOptions{}, "", nil); err != nil {
		t.Fatalf("CrystallizeAndCritique: %v", err)
	}
	if len(defaultProvider.request.Messages) == 0 {
		t.Errorf("default provider was not used when resolver errored")
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func concatEvents(a, b []zeroruntime.StreamEvent, rest ...[]zeroruntime.StreamEvent) []zeroruntime.StreamEvent {
	out := append(a, b...)
	for _, s := range rest {
		out = append(out, s...)
	}
	return out
}
