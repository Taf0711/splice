package splice

import (
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// This file holds the node drawer of the pipeline graph. The terminal pane
// and the web viewer both resolve and word the drawer here, so they show the
// same facts for the same node.

// DrawerStaticTier is the tier the drawer describes when no run exists. It
// matches the `splice config describe` default.
const DrawerStaticTier = schemas.TierStandard

// DrawerTier is the tier the drawer describes: the run tier, or the
// describe default when no run exists.
func DrawerTier(graph presentrun.Graph) schemas.PipelineTier {
	if graph.Live && graph.Tier != "" {
		return schemas.PipelineTier(graph.Tier)
	}
	return DrawerStaticTier
}

// NodeDrawer is the resolved configuration of one drawer node. The
// (Node, Tier, Live) key says what it describes. A drawer whose key does not
// match the graph is stale.
type NodeDrawer struct {
	Node    string
	Tier    string
	Live    bool
	Desc    *NodeDescription
	Problem string
}

// Matches reports whether the drawer describes this node of this graph.
func (d NodeDrawer) Matches(graph presentrun.Graph, node presentrun.GraphNode) bool {
	return d.Node == node.Name && d.Tier == string(DrawerTier(graph)) && d.Live == graph.Live
}

// ResolveNodeDrawer resolves the node's configuration at the graph's tier. It
// reads stage-models.json at stageConfigPath, so the caller runs it outside
// render. topology is the topology the graph came from, or nil.
func ResolveNodeDrawer(graph presentrun.Graph, node presentrun.GraphNode, topology *schemas.PipelineTopology, stageConfigPath string) NodeDrawer {
	tier := DrawerTier(graph)
	drawer := NodeDrawer{Node: node.Name, Tier: string(tier), Live: graph.Live}
	if topology == nil {
		drawer.Problem = "the run topology is not available, so the configuration is unknown"
		return drawer
	}
	if graph.Live && node.Removed != "" {
		drawer.Problem = "removed from this run: " + node.Removed
		return drawer
	}
	if graph.Live && len(node.SkippedAt) > 0 {
		drawer.Problem = "not active at the " + string(tier) + " tier"
		return drawer
	}
	stageConfig, err := schemas.LoadStageModelConfig(stageConfigPath)
	if err != nil {
		drawer.Problem = "stage-models.json did not load: " + err.Error()
		return drawer
	}
	_, nodes, err := DescribeTopology(topology, tier, stageConfig)
	if err != nil {
		drawer.Problem = err.Error()
		return drawer
	}
	for i := range nodes {
		if nodes[i].Name == node.Name {
			drawer.Desc = &nodes[i]
			return drawer
		}
	}
	drawer.Problem = "not active at the " + string(tier) + " tier"
	return drawer
}

// ModelOriginText says where a node's model comes from, in words.
func ModelOriginText(desc NodeDescription) (model, origin string) {
	route := presentrun.RouteText(desc.Model.ProviderProfile, desc.Model.Model, desc.Model.ReasoningEffort)
	switch desc.ModelOrigin {
	case ModelOriginModelFree:
		return "none", "model-free node"
	case ModelOriginNode:
		return route, "node pin in the topology"
	case ModelOriginStage:
		return route, "stage-models.json stage entry"
	case ModelOriginDefault:
		return route, "stage-models.json default"
	case ModelOriginTier:
		return "chosen at run time", "tier resolver (" + desc.TierLabel + " tier)"
	default:
		return "the session model", "primary"
	}
}

// DrawerField is one labeled fact of the node drawer.
type DrawerField struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// NodeDrawerFields lists the drawer facts of a node in reading order. A fact
// with no value is left out. During a run the drawer shows what the run
// applied (the route the node reported when it started), and it labels the
// configuration as configured: escalation, effort fitting, and learned
// budgets can each differ from it.
func NodeDrawerFields(graph presentrun.Graph, node presentrun.GraphNode, drawer NodeDrawer) []DrawerField {
	var fields []DrawerField
	field := func(key, value string) {
		if value != "" {
			fields = append(fields, DrawerField{Key: key, Value: value})
		}
	}
	nodeType := node.Type
	if node.ModelFree {
		nodeType += " (model-free)"
	}
	field("type", nodeType)
	if graph.Live {
		field("status", presentrun.StatusText(node))
		field("tier", drawer.Tier)
	} else {
		// With no run, the drawer resolves at the describe default.
		field("tier", drawer.Tier+" (no run yet)")
	}
	route := node.Route
	applied := graph.Live && route != nil
	if applied && !node.ModelFree && route.Model != "" {
		value := presentrun.RouteText(route.Provider, route.Model, route.Effort)
		if route.Escalated {
			value += " · escalated"
		}
		field("model", value)
	}
	if drawer.Desc != nil {
		model, origin := ModelOriginText(*drawer.Desc)
		if graph.Live {
			field("configured", model+" ("+origin+")")
		} else {
			field("model", model)
			field("origin", origin)
		}
	} else {
		field("config", drawer.Problem)
	}
	if node.ModelFree || (drawer.Desc != nil && drawer.Desc.Caps.ModelFree) {
		field("budget", "none (model-free)")
	} else if use := NodeBudgetUse(graph, node, drawer); use != nil {
		suffix := ""
		if use.Configured {
			suffix = " (configured)"
		}
		field("budget", fmt.Sprintf("input %s / %s · output %s / %s%s",
			presentrun.HumanCount(use.InputUsed), presentrun.HumanCount(use.InputMax),
			presentrun.HumanCount(use.OutputUsed), presentrun.HumanCount(use.OutputMax), suffix))
	}
	field("cost", strings.TrimPrefix(presentrun.CostText(node), "cost "))
	if node.Command != "" {
		field("command", node.Command)
		field("result", presentrun.CommandResult(graph.Live, node))
	}
	if drawer.Desc != nil && drawer.Desc.Agent != nil {
		// An agent node runs a model with tools: say what it can reach.
		agent := drawer.Desc.Agent
		tools := strings.Join(agent.Tools, ", ")
		if len(agent.Marks) > 0 {
			tools += " (" + strings.Join(agent.Marks, ", ") + ")"
		}
		field("tools", tools)
		field("permission", agent.PermissionRule)
		field("turns", fmt.Sprintf("up to %d", agent.MaxTurns))
	}
	if graph.Live && node.Status == presentation.NodeStatusComplete {
		// The files the latest completed pass changed; "none" when it changed
		// nothing. The row is absent while a node has not completed.
		field("changed", presentrun.ChangedFilesText(node.ChangedFiles))
	}
	summary := node.Summary
	switch {
	case summary == "":
		summary = "none yet"
	case node.Passes > 1:
		// Say which pass reported it: a later pass can still be running.
		summary = fmt.Sprintf("from pass %d: %s", node.SummaryIteration+1, summary)
	}
	field("summary", summary)
	return fields
}

// BudgetUse is the token use of a node's latest pass against its budget.
type BudgetUse struct {
	InputUsed  int `json:"input_used"`
	InputMax   int `json:"input_max"`
	OutputUsed int `json:"output_used"`
	OutputMax  int `json:"output_max"`
	// Configured is true when the budget is the configured one: the run has
	// not applied a budget to the node yet.
	Configured bool `json:"configured,omitempty"`
}

// NodeBudgetUse returns the node's use of its budget: the budget that the run
// applied, else the configured budget. It is nil for a model-free node and
// when no budget is known. The drawer text and the web budget bar both read
// it.
func NodeBudgetUse(graph presentrun.Graph, node presentrun.GraphNode, drawer NodeDrawer) *BudgetUse {
	if node.ModelFree || (drawer.Desc != nil && drawer.Desc.Caps.ModelFree) {
		return nil
	}
	use := BudgetUse{InputUsed: int(node.InputTokens), OutputUsed: int(node.OutputTokens)}
	switch {
	case graph.Live && node.Route != nil:
		use.InputMax, use.OutputMax = node.Route.InputBudget, node.Route.OutputBudget
	case drawer.Desc != nil:
		use.InputMax, use.OutputMax, use.Configured = drawer.Desc.Budget.InputMax, drawer.Desc.Budget.OutputMax, true
	default:
		return nil
	}
	return &use
}

// ResolveRunGraphTopology resolves the topology that a run names, so the live
// graph can add the nodes the run tier skipped. A topology that does not
// resolve, or resolves under another name, is not used: note then says why,
// and the graph shows the run plan only.
func ResolveRunGraphTopology(sources TopologySources, name string) (topology *schemas.PipelineTopology, note string) {
	resolved, _, _, err := ResolveTopology(sources)
	switch {
	case err != nil:
		return nil, "the topology did not resolve (" + err.Error() + "); the view shows the run plan only"
	case resolved.Name != name:
		return nil, "the active topology is " + resolved.Name + ", not the run's " + name + "; the view shows the run plan only"
	}
	return resolved, ""
}
