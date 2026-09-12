package splice

import (
	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// effectiveCaps merges a compiled node's capabilities with the stage's own
// runtime capabilities. The topology wins where it declares behavior; timeout
// and description always come from the stage implementation. A legacy plan
// with no compiled caps keeps the stage declaration unchanged.
func effectiveCaps(stage schemas.ExecutionStage, runtime stages.Capabilities) stages.Capabilities {
	if stage.Caps == nil {
		return runtime
	}
	merged := runtime
	merged.ModelFree = stage.Caps.ModelFree
	if stage.Caps.PullContext != nil {
		merged.PullContext = *stage.Caps.PullContext
	}
	if stage.Caps.PullMemory != nil {
		merged.ConsumesMemory = *stage.Caps.PullMemory
	}
	return merged
}

// nodeModelOverride converts a compiled node model to the resolver's override.
func nodeModelOverride(model *schemas.StageModelConfig) agent.ModelOverride {
	if model == nil {
		return agent.ModelOverride{}
	}
	return agent.ModelOverride{
		ProviderProfile: model.ProviderProfile,
		Model:           model.Model,
		ReasoningEffort: model.ReasoningEffort,
	}
}

// planHasVerification reports whether any planned stage produces verification.
// A compiled stage carries its capabilities; a legacy plan falls back to the
// builtin profile for the stage name.
func planHasVerification(plan schemas.ExecutionPlan) bool {
	for _, stage := range plan.Stages {
		if stage.Caps != nil {
			if stage.Caps.ProducesVerification {
				return true
			}
			continue
		}
		if caps, ok := schemas.BuiltinCapabilities(stage.Name); ok && caps.ProducesVerification {
			return true
		}
	}
	return false
}

// stageByPlanName returns the compiled stage with the given name.
func stageByPlanName(plan schemas.ExecutionPlan, name string) (schemas.ExecutionStage, bool) {
	for _, stage := range plan.Stages {
		if stage.Name == name {
			return stage, true
		}
	}
	return schemas.ExecutionStage{}, false
}

// stageModelFree reports whether a planned stage is deterministic. The
// compiled node capabilities are authoritative; a legacy plan with none falls
// back to the builtin profile for the stage name.
func stageModelFree(stage schemas.ExecutionStage) bool {
	if stage.Caps != nil {
		return stage.Caps.ModelFree
	}
	if caps, ok := schemas.BuiltinCapabilities(stage.Name); ok {
		return caps.ModelFree
	}
	return false
}
