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
	gotPlan, gotCritique, err := wf.CrystallizeAndCritique(ctx, events, provider, nil, nil, "", nil)
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
	_, _, err := wf.CrystallizeAndCritique(ctx, events, &fakeWorkflowProvider{}, nil, nil, "", nil)
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
	if _, _, err := wf.CrystallizeAndCritique(ctx, events, provider, nil, nil, "", nil); err != nil {
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

	resolver := func(stage string) (agent.ModelSelection, error) {
		switch stage {
		case "design_crystallize":
			return agent.ModelSelection{Provider: crystallizeProvider, ProviderName: "provider-a", Model: "model-a", ReasoningEffort: "high"}, nil
		case "plan_critic":
			return agent.ModelSelection{Provider: criticProvider, ProviderName: "provider-b", Model: "model-b", ReasoningEffort: "low"}, nil
		default:
			return agent.ModelSelection{}, fmt.Errorf("unknown stage %q", stage)
		}
	}

	wf := NewDesignWorkflow(store, "test-session", "plan-1")
	if _, _, err := wf.CrystallizeAndCritique(ctx, events, &fakeWorkflowProvider{}, resolver, nil, "", nil); err != nil {
		t.Fatalf("CrystallizeAndCritique: %v", err)
	}

	if len(crystallizeProvider.request.Messages) == 0 {
		t.Errorf("crystallize provider was not called")
	}
	if len(criticProvider.request.Messages) == 0 {
		t.Errorf("critic provider was not called")
	}
}

func TestCrystallizeAndCritique_StageStreamFactoryGetsResolvedSelections(t *testing.T) {
	ctx := context.Background()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	if _, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	events := []sessions.Event{
		{Type: sessions.EventDesignModeEntered},
		{Type: sessions.EventMessage, Payload: mustMarshal(t, map[string]string{"role": "user", "content": "do it"})},
	}
	planArgs, _ := json.Marshal(validDesignPlan("do it"))
	critiqueArgs, _ := json.Marshal(validCritique("fine"))
	crystallizeProvider := &fakeWorkflowProvider{events: concatEvents(workflowToolCall("plan", "submit_design_plan", string(planArgs)), workflowDone())}
	criticProvider := &fakeWorkflowProvider{events: concatEvents(workflowToolCall("critique", "submit_critique", string(critiqueArgs)), workflowDone())}
	resolver := func(stage string) (agent.ModelSelection, error) {
		if stage == "design_crystallize" {
			return agent.ModelSelection{Provider: crystallizeProvider, ProviderName: "provider-a", Model: "model-a"}, nil
		}
		return agent.ModelSelection{Provider: criticProvider, ProviderName: "provider-b", Model: "model-b"}, nil
	}
	var got []struct {
		stage     string
		selection agent.ModelSelection
	}
	factory := StageStreamFactory(func(stage string, selection agent.ModelSelection) zeroruntime.CollectOptions {
		got = append(got, struct {
			stage     string
			selection agent.ModelSelection
		}{stage: stage, selection: selection})
		return zeroruntime.CollectOptions{}
	})
	wf := NewDesignWorkflow(store, "test-session", "plan-1")
	if _, _, err := wf.CrystallizeAndCritique(ctx, events, &fakeWorkflowProvider{}, resolver, factory, "", nil); err != nil {
		t.Fatalf("CrystallizeAndCritique: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("factory calls = %d, want 2", len(got))
	}
	if got[0].stage != "design_crystallize" || got[1].stage != "plan_critic" {
		t.Fatalf("factory stages = %#v, want design_crystallize then plan_critic", got)
	}
	if got[0].selection.Model != "model-a" || got[0].selection.ProviderName != "provider-a" {
		t.Fatalf("crystallize selection = %+v", got[0].selection)
	}
	if got[1].selection.Model != "model-b" || got[1].selection.ProviderName != "provider-b" {
		t.Fatalf("critic selection = %+v", got[1].selection)
	}
}

func TestCrystallizeAndCritique_NilStageStreamFactoryPreservesBehavior(t *testing.T) {
	ctx := context.Background()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	if _, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	events := []sessions.Event{
		{Type: sessions.EventDesignModeEntered},
		{Type: sessions.EventMessage, Payload: mustMarshal(t, map[string]string{"role": "user", "content": "do it"})},
	}
	planArgs, _ := json.Marshal(validDesignPlan("do it"))
	critiqueArgs, _ := json.Marshal(validCritique("fine"))
	provider := &fakeWorkflowProvider{events: concatEvents(
		workflowToolCall("plan", "submit_design_plan", string(planArgs)),
		workflowToolCall("critique", "submit_critique", string(critiqueArgs)),
		workflowDone(),
	)}
	wf := NewDesignWorkflow(store, "test-session", "plan-1")
	if _, _, err := wf.CrystallizeAndCritique(ctx, events, provider, nil, nil, "", nil); err != nil {
		t.Fatalf("CrystallizeAndCritique: %v", err)
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
	if _, _, err := wf.CrystallizeAndCritique(ctx, events, provider, nil, nil, "", nil); err != nil {
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
	if _, _, err := wf.CrystallizeAndCritique(ctx, events, provider2, nil, nil, "", nil); err != nil {
		t.Fatalf("second CrystallizeAndCritique: %v", err)
	}
	var criticPayload string
	for _, message := range provider2.request.Messages {
		if message.Role == zeroruntime.MessageRoleUser {
			criticPayload = message.Content
			break
		}
	}
	var criticInput schemas.PlanCriticInput
	if err := json.Unmarshal([]byte(criticPayload), &criticInput); err != nil {
		t.Fatalf("unmarshal second critic payload: %v", err)
	}
	if criticInput.PreviousCritique == nil || criticInput.PreviousCritique.OverallAssessment != "fine" {
		t.Fatalf("second critic payload lost prior critique: %#v", criticInput.PreviousCritique)
	}
	if criticInput.PreviousPlan == nil || criticInput.PreviousPlan.Epic != "do it" {
		t.Fatalf("second critic payload lost prior plan: %#v", criticInput.PreviousPlan)
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
	gotPlan, gotCritique, err := wf.CrystallizeAndCritique(ctx, events, provider, nil, nil, "", nil)
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

	resolver := func(stage string) (agent.ModelSelection, error) {
		return agent.ModelSelection{}, errors.New("resolver unavailable")
	}

	wf := NewDesignWorkflow(store, "test-session", "plan-1").WithPrimarySelection("primary", "primary-model", "medium")
	selection := wf.resolveProvider(defaultProvider, resolver, "design_crystallize")
	if selection.Provider != defaultProvider || selection.ProviderName != "primary" || selection.Model != "primary-model" || selection.ReasoningEffort != "medium" {
		t.Fatalf("fallback selection = %+v", selection)
	}
	if _, _, err := wf.CrystallizeAndCritique(ctx, events, defaultProvider, resolver, nil, "", nil); err != nil {
		t.Fatalf("CrystallizeAndCritique: %v", err)
	}
	if len(defaultProvider.request.Messages) == 0 {
		t.Errorf("default provider was not used when resolver errored")
	}
}

// TestPlanCriticInputLeavesPipelineFieldsEmpty proves plan_critic's
// HarnessStageInput (constructed in design_workflow.go, outside any tier
// pipeline) carries no PipelineStages/NextStage roster, and that leaving
// them empty still validates.
func TestPlanCriticInputLeavesPipelineFieldsEmpty(t *testing.T) {
	criticInput := schemas.HarnessStageInput{
		RunID:         "plan-1",
		StageName:     "plan_critic",
		Sequence:      1,
		PlanTier:      schemas.TierArchitectural,
		RequestIntent: "build a thing",
	}
	if len(criticInput.PipelineStages) != 0 || criticInput.NextStage != "" {
		t.Fatalf("expected empty pipeline roster, got PipelineStages=%#v NextStage=%q", criticInput.PipelineStages, criticInput.NextStage)
	}
	if err := criticInput.Validate(); err != nil {
		t.Fatalf("plan_critic input with empty pipeline fields should validate: %v", err)
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
