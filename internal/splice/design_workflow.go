package splice

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// DesignWorkflow orchestrates crystallization and critique for one design
// epoch. It owns: history mapping, provider routing, typed results, and
// lifecycle event persistence. The TUI calls it; it does not call the TUI.
type DesignWorkflow struct {
	store            *sessions.Store
	sessionID        string
	planID           string
	primarySelection agent.ModelSelection
}

// StageStreamFactory returns the stream callbacks for one design stage. It is
// called after the stage model is resolved, so the caller can attribute usage
// to the model that the stage really uses. A nil factory, or a nil return,
// disables the stream for that stage.
type StageStreamFactory func(stageName string, selection agent.ModelSelection) zeroruntime.CollectOptions

// NewDesignWorkflow creates a workflow bound to a session store + session ID.
// planID identifies this plan revision family within the session. The TUI
// generates a unique planID per crystallization (e.g. "plan-<timestamp>").
func NewDesignWorkflow(store *sessions.Store, sessionID, planID string) *DesignWorkflow {
	return &DesignWorkflow{
		store:     store,
		sessionID: sessionID,
		planID:    planID,
	}
}

// WithPrimarySelection sets the identity and effort of the default route.
func (w *DesignWorkflow) WithPrimarySelection(providerName, model, effort string) *DesignWorkflow {
	w.primarySelection.ProviderName = providerName
	w.primarySelection.Model = model
	w.primarySelection.ReasoningEffort = effort
	return w
}

// CrystallizeAndCritique maps session events to conversation history,
// crystallizes a DesignPlan, runs the adversarial critic, and persists
// plan_crystallized + critique_recorded events. Returns the plan and critique.
//
// provider is the default (active) provider. stageModelResolver is optional
// (may be nil): when set, it resolves per-stage providers for the
// "design_crystallize" and "plan_critic" stages; when nil or when it
// returns nil, the default provider is used. streamFactory supplies callbacks
// for each resolved stage.
//
// workDir and images are passed through to the stage options.
func (w *DesignWorkflow) CrystallizeAndCritique(
	ctx context.Context,
	events []sessions.Event,
	provider agent.Provider,
	stageModelResolver agent.StageModelResolver,
	streamFactory StageStreamFactory,
	workDir string,
	images []zeroruntime.ImageBlock,
) (schemas.DesignPlan, schemas.PlanCritique, error) {
	history := MapDesignHistory(events)
	if len(history) == 0 {
		return schemas.DesignPlan{}, schemas.PlanCritique{}, fmt.Errorf("crystallize requires at least one conversation message")
	}

	input := schemas.DesignConversationInput{History: history}
	if err := input.Validate(); err != nil {
		return schemas.DesignPlan{}, schemas.PlanCritique{}, fmt.Errorf("crystallize input: %w", err)
	}

	selection := w.resolveProvider(provider, stageModelResolver, "design_crystallize")
	var stream zeroruntime.CollectOptions
	if streamFactory != nil {
		stream = streamFactory("design_crystallize", selection)
	}
	opts := stages.StageOptions{
		WorkDir:         workDir,
		Stream:          stream,
		Images:          images,
		ModelOverride:   selection.Model,
		ReasoningEffort: selection.ReasoningEffort,
	}

	plan, err := stages.DesignCrystallizer{}.Crystallize(ctx, selection.Provider, opts, input)
	if err != nil {
		return schemas.DesignPlan{}, schemas.PlanCritique{}, err
	}

	revision := 1
	var previousPlan *schemas.DesignPlan
	var previousCritique *schemas.PlanCritique
	if state, err := ReconstructDesignState(events); err == nil {
		if state.Revision.PlanID == w.planID {
			revision = state.Revision.Revision + 1
		}
		previousPlan = state.Plan
		previousCritique = state.Critique
	}

	planJSON, err := json.Marshal(plan)
	if err != nil {
		return schemas.DesignPlan{}, schemas.PlanCritique{}, fmt.Errorf("marshal crystallized plan: %w", err)
	}

	if _, err := w.store.AppendEvent(w.sessionID, sessions.AppendEventInput{
		Type:    sessions.EventPlanCrystallized,
		Payload: PlanCrystallizedPayload{PlanID: w.planID, Revision: revision, Plan: planJSON},
	}); err != nil {
		return schemas.DesignPlan{}, schemas.PlanCritique{}, fmt.Errorf("persist plan_crystallized: %w", err)
	}

	criticSelection := w.resolveProvider(provider, stageModelResolver, "plan_critic")
	var criticStream zeroruntime.CollectOptions
	if streamFactory != nil {
		criticStream = streamFactory("plan_critic", criticSelection)
	}
	criticOpts := stages.StageOptions{
		WorkDir:          workDir,
		Stream:           criticStream,
		Images:           images,
		Plan:             &plan,
		PreviousPlan:     previousPlan,
		PreviousCritique: previousCritique,
	}
	criticOpts.ModelOverride = criticSelection.Model
	criticOpts.ReasoningEffort = criticSelection.ReasoningEffort

	criticInput := schemas.HarnessStageInput{
		RunID:         w.planID,
		StageName:     "plan_critic",
		Sequence:      1,
		PlanTier:      schemas.TierArchitectural,
		RequestIntent: plan.Epic,
		// PipelineStages and NextStage stay empty: plan_critic runs in the
		// design phase, not a tier pipeline, so no roster applies.
	}

	output, err := stages.PlanCritic{}.Run(ctx, criticInput, criticSelection.Provider, criticOpts)
	if err != nil {
		return plan, schemas.PlanCritique{}, err
	}

	critique, err := stages.ExtractPlanCritique(output)
	if err != nil {
		return plan, schemas.PlanCritique{}, err
	}

	critiqueJSON, err := json.Marshal(critique)
	if err != nil {
		return plan, schemas.PlanCritique{}, fmt.Errorf("marshal critique: %w", err)
	}

	if _, err := w.store.AppendEvent(w.sessionID, sessions.AppendEventInput{
		Type:    sessions.EventCritiqueRecorded,
		Payload: CritiqueRecordedPayload{PlanID: w.planID, Revision: revision, Critique: critiqueJSON},
	}); err != nil {
		return plan, critique, fmt.Errorf("persist critique_recorded: %w", err)
	}

	return plan, critique, nil
}

func (w *DesignWorkflow) resolveProvider(
	defaultProvider agent.Provider,
	resolver agent.StageModelResolver,
	stageName string,
) agent.ModelSelection {
	if resolver != nil {
		selection, err := resolver(stageName)
		if err == nil && selection.Provider != nil {
			return selection
		}
	}
	selection := w.primarySelection
	selection.Provider = defaultProvider
	return selection
}
