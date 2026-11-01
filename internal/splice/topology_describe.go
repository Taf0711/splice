package splice

import (
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// NodeDescription is the resolved configuration of one active node at one
// tier: the model route and the rung that supplied it, the budget, and the
// capabilities. `splice config describe` and the TUI node drawer both read it,
// so the two surfaces cannot disagree.
type NodeDescription struct {
	Name  string
	Type  string
	Tiers []schemas.PipelineTier
	// Model is the declared route. It is empty when the origin is the tier
	// resolver, the primary model, or model-free.
	Model schemas.StageModelConfig
	// ModelOrigin is a ModelOrigin* rung, or "model-free".
	ModelOrigin string
	// TierLabel is the model tier label the tier resolver uses. It is empty
	// for a model-free node.
	TierLabel string
	Budget    schemas.StageBudget
	Caps      schemas.NodeCapabilities
	// Command is the exact command line of a command node.
	Command string
	// Agent is the disclosure of an agent node: tools, permission, turns,
	// budget, and prompt preview. It is nil for every other node type.
	Agent *AgentNodeDisclosure
}

// ModelOriginModelFree is the origin of a node that never resolves a model.
const ModelOriginModelFree = "model-free"

// DescribeTopology compiles a topology at one tier and resolves every active
// node through the same ladder the executor uses. It does no I/O: the caller
// loads stage-models.json.
func DescribeTopology(topology *schemas.PipelineTopology, tier schemas.PipelineTier, stageConfig schemas.StageModelConfigFile) (CompiledTopology, []NodeDescription, error) {
	compiled, err := CompileTopology(topology, tier)
	if err != nil {
		return CompiledTopology{}, nil, err
	}
	labels := StageTierLabelsFor(topology)
	nodesByName := make(map[string]schemas.PipelineNode, len(topology.Nodes))
	for _, node := range topology.Nodes {
		nodesByName[node.Name] = node
	}
	descriptions := make([]NodeDescription, 0, len(compiled.Stages))
	for _, stage := range compiled.Stages {
		node := nodesByName[stage.Name]
		caps := schemas.NodeCapabilities{}
		if stage.Caps != nil {
			caps = *stage.Caps
		}
		description := NodeDescription{
			Name:      stage.Name,
			Type:      node.Type,
			Tiers:     append([]schemas.PipelineTier(nil), node.Tiers...),
			TierLabel: labels[stage.Name],
			Budget:    stage.Budget,
			Caps:      caps,
		}
		if caps.ModelFree {
			// A model-free node never resolves a model, so a route would be noise.
			description.ModelOrigin = ModelOriginModelFree
		} else {
			description.Model, description.ModelOrigin = ResolveModelRoute(stage.Name, stage.Model, stageConfig, description.TierLabel != "")
		}
		if node.Type == schemas.NodeTypeCommand {
			description.Command = stages.CommandLine(node.Command)
		}
		if node.Type == schemas.NodeTypeAgent {
			agent := DescribeAgentNode(node)
			description.Agent = &agent
		}
		descriptions = append(descriptions, description)
	}
	return compiled, descriptions, nil
}

// TopologyWarnings compiles a topology at every tier and returns each
// distinct compile warning once, prefixed with the tiers it applies to. The
// order is the order of first appearance, smallest tier first.
func TopologyWarnings(topology *schemas.PipelineTopology) ([]string, error) {
	var order []string
	tiers := map[string][]string{}
	for _, tier := range pipelineTiers {
		compiled, err := CompileTopology(topology, tier)
		if err != nil {
			return nil, err
		}
		for _, warning := range compiled.Warnings {
			if _, seen := tiers[warning]; !seen {
				order = append(order, warning)
			}
			tiers[warning] = append(tiers[warning], string(compiled.Tier))
		}
	}
	out := make([]string, 0, len(order))
	for _, warning := range order {
		out = append(out, "at "+strings.Join(tiers[warning], ", ")+": "+warning)
	}
	return out, nil
}
