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

// stageCapabilities returns the builtin capability profile for a planned stage
// that carries no compiled capabilities. The node type is authoritative; the
// name is the fallback for a plan built before node types were carried, where
// name and type are the same.
func stageCapabilities(stage schemas.ExecutionStage) (schemas.NodeCapabilities, bool) {
	if stage.Type != "" {
		if caps, ok := schemas.BuiltinCapabilities(stage.Type); ok {
			return caps, true
		}
	}
	return schemas.BuiltinCapabilities(stage.Name)
}

// planHasVerification reports whether any planned stage produces verification.
// A compiled stage carries its capabilities; a legacy plan falls back to the
// builtin profile for the stage type, then the stage name.
func planHasVerification(plan schemas.ExecutionPlan) bool {
	for _, stage := range plan.Stages {
		if stage.Caps != nil {
			if stage.Caps.ProducesVerification {
				return true
			}
			continue
		}
		if caps, ok := stageCapabilities(stage); ok && caps.ProducesVerification {
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

// roleStageName returns the plan stage name for a builtin role (a builtin node
// type such as "code_writer"). A topology may rename a builtin node, so the
// plan stage carries the role in Type and the node name in Name. A plan that
// predates node types has no Type, so its stage name is the role. An absent
// role, or a plan without the role, falls back to the role name.
func roleStageName(plan schemas.ExecutionPlan, role string) string {
	for _, stage := range plan.Stages {
		if stage.Type == role {
			return stage.Name
		}
	}
	return role
}

// codeWriterName returns the plan name of the code writer stage, or "" when
// the plan has no code writer, such as an agent-only topology.
func codeWriterName(plan schemas.ExecutionPlan) string {
	for _, stage := range plan.Stages {
		if stageRole(stage) == "code_writer" {
			return stage.Name
		}
	}
	return ""
}

// boundaryStage labels a trajectory boundary snapshot: the code writer, or
// "pass" when the plan has no code writer.
func boundaryStage(plan schemas.ExecutionPlan) string {
	if name := codeWriterName(plan); name != "" {
		return name
	}
	return "pass"
}

// stageRole returns the builtin role of a planned stage: its node type, or its
// name for a plan that predates node types.
func stageRole(stage schemas.ExecutionStage) string {
	if stage.Type != "" {
		return stage.Type
	}
	return stage.Name
}

// writerStageName is the plan name of the code writer when a topology renames
// it, or empty when the writer keeps its builtin name.
func writerStageName(plan schemas.ExecutionPlan) string {
	if name := roleStageName(plan, "code_writer"); name != "code_writer" {
		return name
	}
	return ""
}

// withRoleAlias adds the builtin role key to a map of prior stage output when a
// topology renames that role. A stage that reads prior output by its builtin
// name (test_generator reads the code_writer summary) keeps working. The map
// is copied, never changed in place.
func withRoleAlias[V any](m map[string]V, plan schemas.ExecutionPlan, role string) map[string]V {
	name := roleStageName(plan, role)
	v, ok := m[name]
	if name == role || !ok {
		return m
	}
	out := make(map[string]V, len(m)+1)
	for k, val := range m {
		out[k] = val
	}
	out[role] = v
	return out
}

// stageModelFree reports whether a planned stage is deterministic. The
// compiled node capabilities are authoritative; a legacy plan with none falls
// back to the builtin profile for the stage type, then the stage name.
func stageModelFree(stage schemas.ExecutionStage) bool {
	if stage.Caps != nil {
		return stage.Caps.ModelFree
	}
	if caps, ok := stageCapabilities(stage); ok {
		return caps.ModelFree
	}
	return false
}
