package presentrun

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// Graph is the view model of one pipeline graph. It is plain data with no
// styling, so the terminal view and the web viewer render the same truth.
type Graph struct {
	// Topology names the topology. It is empty when only the run plan is known.
	Topology string `json:"topology,omitempty"`
	// Tier is the run tier. It is empty for the static view.
	Tier string `json:"tier,omitempty"`
	// Live is true when the graph projects a run.
	Live bool `json:"live"`
	// Nodes are ordered by dependency depth, then by declaration order.
	Nodes []GraphNode `json:"nodes"`
	// Notes are notices about the view itself.
	Notes []string `json:"notes,omitempty"`
}

// GraphNode is one row of the graph. Identity is (Name, Iteration): a node
// that ran again shows its latest pass.
type GraphNode struct {
	Name string `json:"name"`
	// Type is the topology node type. It is empty when only the plan is known.
	Type  string                `json:"type,omitempty"`
	Kind  presentation.NodeKind `json:"kind"`
	Depth int                   `json:"depth"`
	// Status is empty in the static view, for a node that did not start,
	// and for a node the run tier skipped.
	Status    presentation.NodeStatus `json:"status,omitempty"`
	Iteration int                     `json:"iteration"`
	// Passes counts the passes the run reported for this node.
	Passes int `json:"passes"`
	// InputTokens and OutputTokens are the latest pass: the pass that the
	// applied budget (Route) belongs to.
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	// Tokens and CostUSD sum every pass of the node.
	Tokens  int64   `json:"tokens"`
	CostUSD float64 `json:"cost_usd"`
	// CostUnpriced is true when a request of any pass had no price, so
	// CostUSD is a lower bound.
	CostUnpriced bool     `json:"cost_unpriced,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	// SkippedAt lists the tiers where the node is not active. In a live view
	// it holds the run tier when the run skipped the node.
	SkippedAt []string `json:"skipped_at,omitempty"`
	// Removed is why the run removed the node although its tier was active,
	// for example "flag splice.security_auditor is off". A removed node has
	// no SkippedAt.
	Removed   string `json:"removed,omitempty"`
	ModelFree bool   `json:"model_free,omitempty"`
	// Command is the exact command line of a command node.
	Command string `json:"command,omitempty"`
	Summary string `json:"summary,omitempty"`
	// ChangedFiles are the files the node's latest completed pass reported.
	// A completed node with none changed nothing.
	ChangedFiles []string `json:"changed_files,omitempty"`
	// SummaryIteration is the 0-based pass that reported Summary. It can be
	// older than Iteration while a later pass runs.
	SummaryIteration int `json:"summary_iteration,omitempty"`
	// NotRun is true for a planned node that never started in a run that
	// has ended. It is not pending: the run will not reach it.
	NotRun bool `json:"not_run,omitempty"`
	// Interrupted is true for a node that was still running when the run
	// ended, for example when the user cancelled the run.
	Interrupted bool `json:"interrupted,omitempty"`
	// Route is what the latest pass applied: the model the run sent, the
	// fitted effort, the applied budget, and whether escalation chose the
	// model. It is nil until the node starts.
	Route *presentation.NodeRoute `json:"route,omitempty"`
}

// graphTiers is every pipeline tier in size order.
var graphTiers = []schemas.PipelineTier{
	schemas.TierTrivial,
	schemas.TierLight,
	schemas.TierStandard,
	schemas.TierSubstantial,
	schemas.TierArchitectural,
}

// topologyChangedNote is the notice for a run whose plan no longer matches
// the topology on disk. The view then shows the run plan only.
const topologyChangedNote = "the active topology changed after this run started; the view shows the run plan only"

// BuildGraph builds the graph view model. It is pure: no I/O, no clock.
//
// With an empty state it is the static view of the topology. With a run
// state it is the live view: the run plan is the truth for which nodes run
// and what they wait for, and the topology adds the nodes the run tier
// skipped, the node types, and the command lines. When the plan does not
// match the topology, the topology is dropped and Notes says so. A nil
// topology with no run is an error.
func BuildGraph(topology *schemas.PipelineTopology, state presentation.State) (Graph, error) {
	live := len(state.Plan.Stages) > 0 || len(state.Nodes) > 0
	if topology == nil && !live {
		return Graph{}, fmt.Errorf("pipeline graph: no topology and no run plan")
	}
	graph := Graph{Live: live, Tier: state.Plan.Tier}
	if !live {
		graph.Tier = ""
	}

	// Latest pass per node name, and the pass count.
	latest := map[string]presentation.ExecutionNode{}
	passes := map[string]int{}
	// total sums the spend of every pass of a node.
	total := map[string]presentation.CostSummary{}
	// summary is the newest pass that reported one: a pass still running has
	// none yet, so the earlier pass's summary stays visible.
	summary := map[string]presentation.ExecutionNode{}
	var runOrder []string
	for _, node := range state.Nodes {
		prior, seen := latest[node.ID]
		if !seen {
			runOrder = append(runOrder, node.ID)
		}
		passes[node.ID]++
		sum := total[node.ID]
		sum.Tokens += node.Cost.Tokens
		sum.USD += node.Cost.USD
		sum.Unpriced = sum.Unpriced || node.Cost.Unpriced
		total[node.ID] = sum
		if !seen || node.Iteration >= prior.Iteration {
			latest[node.ID] = node
		}
		if prior, ok := summary[node.ID]; node.Summary != "" && (!ok || node.Iteration >= prior.Iteration) {
			summary[node.ID] = node
		}
	}
	planRoster := append([]string(nil), state.Plan.Stages...)
	for _, name := range runOrder {
		if !slices.Contains(planRoster, name) {
			planRoster = append(planRoster, name)
		}
	}
	planDeps := func(name string) []string {
		if node, ok := latest[name]; ok && len(node.Dependencies) > 0 {
			return node.Dependencies
		}
		return state.Plan.Dependencies[name]
	}

	if topology != nil && live && !planMatchesTopology(topology, planRoster, planDeps) {
		graph.Notes = append(graph.Notes, topologyChangedNote)
		topology = nil
	}

	var roster []string
	deps := map[string][]string{}
	byName := map[string]schemas.PipelineNode{}
	if topology != nil {
		graph.Topology = topology.Name
		for _, node := range topology.Nodes {
			roster = append(roster, node.Name)
			byName[node.Name] = node
		}
		for _, edge := range topology.Edges {
			deps[edge.To] = append(deps[edge.To], edge.From)
		}
	} else {
		graph.Topology = state.Plan.Topology
		roster = planRoster
		for _, name := range roster {
			deps[name] = append([]string(nil), planDeps(name)...)
		}
	}

	depth, err := graphDepths(roster, deps)
	if err != nil {
		return Graph{}, err
	}
	active := map[string]bool{}
	for _, name := range planRoster {
		active[name] = true
	}

	nodes := make([]GraphNode, 0, len(roster))
	for _, name := range roster {
		row := GraphNode{
			Name:         name,
			Kind:         NodeKindForStage(name),
			Depth:        depth[name],
			Dependencies: append([]string(nil), deps[name]...),
		}
		if node, ok := byName[name]; ok {
			row.Type = node.Type
			row.Kind = NodeKindForStage(node.Type)
			row.ModelFree = node.EffectiveCapabilities().ModelFree
			if node.Type == schemas.NodeTypeCommand {
				row.Command = stages.CommandLine(node.Command)
			}
			if !live {
				for _, tier := range graphTiers {
					if !node.ActiveAtTier(tier) {
						row.SkippedAt = append(row.SkippedAt, string(tier))
					}
				}
			}
		}
		if live && !active[name] {
			if reason := state.Plan.Removed[name]; reason != "" {
				row.Removed = reason
			} else {
				tier := state.Plan.Tier
				if tier == "" {
					tier = "the run tier"
				}
				row.SkippedAt = []string{tier}
			}
		}
		if node, ok := latest[name]; ok {
			if _, typed := byName[name]; !typed {
				row.Kind = node.Kind
			}
			row.Status = node.Status
			row.Iteration = node.Iteration
			row.Passes = passes[name]
			row.InputTokens = node.Usage.InputTokens
			row.OutputTokens = node.Usage.OutputTokens
			row.Tokens = total[name].Tokens
			row.CostUSD = total[name].USD
			row.CostUnpriced = total[name].Unpriced
			row.Summary = summary[name].Summary
			row.ChangedFiles = node.ChangedFiles
			row.SummaryIteration = summary[name].Iteration
			if node.Route != nil {
				route := *node.Route
				row.Route = &route
			}
		}
		if live && state.Completion != nil && row.Status == presentation.NodeStatusRunning {
			row.Interrupted = true
		}
		if live && state.Completion != nil && row.Status == "" && len(row.SkippedAt) == 0 && row.Removed == "" {
			row.NotRun = true
		}
		nodes = append(nodes, row)
	}
	// Stable sort keeps declaration order inside one depth.
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Depth < nodes[j].Depth })
	graph.Nodes = nodes
	return graph, nil
}

// planMatchesTopology reports whether every planned stage is a topology node
// with the same dependencies. A mismatch means the topology on disk is not
// the one the run compiled.
func planMatchesTopology(topology *schemas.PipelineTopology, roster []string, planDeps func(string) []string) bool {
	nodes := map[string]bool{}
	for _, node := range topology.Nodes {
		nodes[node.Name] = true
	}
	edges := map[string][]string{}
	for _, edge := range topology.Edges {
		edges[edge.To] = append(edges[edge.To], edge.From)
	}
	for _, name := range roster {
		if !nodes[name] {
			return false
		}
		want := slices.Clone(edges[name])
		got := slices.Clone(planDeps(name))
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(want, got) {
			return false
		}
	}
	return true
}

// graphDepths returns each node's dependency depth: 0 with no dependency,
// otherwise one more than its deepest dependency. A dependency on an unknown
// node or a cycle is an error that names the nodes.
func graphDepths(roster []string, deps map[string][]string) (map[string]int, error) {
	known := make(map[string]bool, len(roster))
	for _, name := range roster {
		known[name] = true
	}
	depth := make(map[string]int, len(roster))
	visiting := map[string]bool{}
	var visit func(name string, path []string) (int, error)
	visit = func(name string, path []string) (int, error) {
		if d, ok := depth[name]; ok {
			return d, nil
		}
		if visiting[name] {
			return 0, fmt.Errorf("pipeline graph: cycle among nodes %s", strings.Join(append(path, name), " -> "))
		}
		visiting[name] = true
		d := 0
		for _, dep := range deps[name] {
			if !known[dep] {
				return 0, fmt.Errorf("pipeline graph: node %s depends on unknown node %s", name, dep)
			}
			dd, err := visit(dep, append(path, name))
			if err != nil {
				return 0, err
			}
			d = max(d, dd+1)
		}
		visiting[name] = false
		depth[name] = d
		return d, nil
	}
	for _, name := range roster {
		if _, err := visit(name, nil); err != nil {
			return nil, err
		}
	}
	return depth, nil
}
