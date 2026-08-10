package splice

import (
	"fmt"

	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// DesignContext is the merged project context for design agents. It combines
// the current design epoch conversation history with the latest persisted
// DesignPlan and PlanCritique, with optional live overlays applied.
type DesignContext struct {
	History         []schemas.ConversationMessage
	CurrentPlan     *schemas.DesignPlan
	CurrentCritique *schemas.PlanCritique
	// State is the full reconstructed design state. Consumers use it for the
	// revision number and task outcomes without replaying the events again.
	State DesignState
}

// AssembleDesignContext merges the design epoch conversation history with the
// latest persisted plan and critique. livePlan and liveCritique are optional:
// when set, they win over the persisted values. The TUI uses them to carry
// in-memory plan or critique state that failed to persist in the current
// process, for example a critique_recorded write that errored. Malformed
// lifecycle events return a named error; nothing silently defaults.
func AssembleDesignContext(events []sessions.Event, livePlan *schemas.DesignPlan, liveCritique *schemas.PlanCritique) (DesignContext, error) {
	state, err := ReconstructDesignState(events)
	if err != nil {
		return DesignContext{}, fmt.Errorf("assemble design context: %w", err)
	}
	ctx := DesignContext{
		History:         MapDesignHistory(events),
		CurrentPlan:     state.Plan,
		CurrentCritique: state.Critique,
		State:           state,
	}
	if livePlan != nil {
		ctx.CurrentPlan = livePlan
	}
	if liveCritique != nil {
		ctx.CurrentCritique = liveCritique
	}
	return ctx, nil
}
