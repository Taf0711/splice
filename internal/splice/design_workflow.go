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
	store        *sessions.Store
	sessionID    string
	planID       string
	primaryModel string
}

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

// WithPrimaryModel sets the model ID of the default provider. It is used to
// seed ModelOverride when the composed stage resolver falls back to the
// primary, so that design-phase stage errors are attributed to the real
// primary model instead of a literal tier label.
func (w *DesignWorkflow) WithPrimaryModel(model string) *DesignWorkflow {
	w.primaryModel = model
	return w
}

// CrystallizeAndCritique maps session events to conversation history,
// crystallizes a DesignPlan, runs the adversarial critic, and persists
// plan_crystallized + critique_recorded events. Returns the plan and critique.
//
// provider is the default (active) provider. stageModelResolver is optional
// (may be nil): when set, it resolves per-stage providers for the
// "design_crystallize" and "plan_critic" stages; when nil or when it
// returns nil, the default provider is used. stream carries TUI callbacks.
//
// workDir and images are passed through to the stage options.
func (w *DesignWorkflow) CrystallizeAndCritique(
	ctx context.Context,
	events []sessions.Event,
	provider agent.Provider,
	stageModelResolver func(string) (agent.Provider, string, string, error),
	stream zeroruntime.CollectOptions,
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

	resolvedProvider, model, effort := w.resolveProvider(provider, stageModelResolver, "design_crystallize")
	opts := stages.StageOptions{
		WorkDir:         workDir,
		Stream:          stream,
		Images:          images,
		ModelOverride:   model,
		ReasoningEffort: effort,
	}

	plan, err := stages.DesignCrystallizer{}.Crystallize(ctx, resolvedProvider, opts, input)
	if err != nil {
		return schemas.DesignPlan{}, schemas.PlanCritique{}, err
	}

	revision := 1
	if state, err := ReconstructDesignState(events); err == nil && state.Revision.PlanID == w.planID {
		revision = state.Revision.Revision + 1
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

	criticOpts := stages.StageOptions{
		WorkDir: workDir,
		Stream:  stream,
		Images:  images,
		Plan:    &plan,
	}
	criticProvider, criticModel, criticEffort := w.resolveProvider(provider, stageModelResolver, "plan_critic")
	criticOpts.ModelOverride = criticModel
	criticOpts.ReasoningEffort = criticEffort

	criticInput := schemas.HarnessStageInput{
		RunID:         w.planID,
		StageName:     "plan_critic",
		Sequence:      1,
		PlanTier:      schemas.TierArchitectural,
		RequestIntent: plan.Epic,
	}

	output, err := stages.PlanCritic{}.Run(ctx, criticInput, criticProvider, criticOpts)
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
	resolver func(string) (agent.Provider, string, string, error),
	stageName string,
) (agent.Provider, string, string) {
	if resolver != nil {
		p, m, e, err := resolver(stageName)
		if err == nil && p != nil {
			return p, m, e
		}
	}
	if w.primaryModel != "" {
		return defaultProvider, w.primaryModel, ""
	}
	return defaultProvider, "", ""
}
