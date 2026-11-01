package splice

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// CompiledTopology is the executable shape of one topology at one tier.
type CompiledTopology struct {
	Name string
	// Tier is the tier this topology compiled for. TopologyWarnings reads it
	// to name the tiers of each compile warning in the /pipeline view. It is
	// registered in internal/wiring.
	Tier     schemas.PipelineTier
	Stages   []schemas.ExecutionStage
	Budget   schemas.TokenBudget
	Warnings []string
	// TierInput and TierOutput are the tier envelope that the non-agent nodes
	// must fit. AgentInput and AgentOutput are the explicit budgets of the
	// agent nodes. Those are added on top of the envelope, not drawn from it,
	// so Budget holds the sum of both.
	TierInput   int
	TierOutput  int
	AgentInput  int
	AgentOutput int
}

// defaultTopology returns the embedded default pipeline as a topology. The
// graph is the compiled form of today's stage roster. The edges follow one
// rule: a node is active at every tier where its dependents are active, so
// tier filtering never leaves a dangling dependency.
// DefaultTopologyName is the name of the embedded topology. The name is
// reserved for it: a library entry or an imported topology cannot use it.
const DefaultTopologyName = "default"

func defaultTopology() *schemas.PipelineTopology {
	standardPlus := []schemas.PipelineTier{schemas.TierStandard, schemas.TierSubstantial, schemas.TierArchitectural}
	lightPlus := []schemas.PipelineTier{schemas.TierLight, schemas.TierStandard, schemas.TierSubstantial, schemas.TierArchitectural}
	substantialPlus := []schemas.PipelineTier{schemas.TierSubstantial, schemas.TierArchitectural}
	return &schemas.PipelineTopology{
		Version:     schemas.TopologySchemaVersion,
		Name:        DefaultTopologyName,
		Description: "The embedded Splice pipeline: write, generate tests, analyze, audit, test, verify.",
		Nodes: []schemas.PipelineNode{
			{Name: "code_writer", Type: "code_writer"},
			{Name: "test_generator", Type: "test_generator", Tiers: standardPlus},
			{Name: "static_analyzer", Type: "static_analyzer", Tiers: lightPlus},
			{Name: "security_auditor", Type: "security_auditor", Tiers: substantialPlus},
			{Name: "test_runner", Type: "test_runner", Tiers: lightPlus},
			{Name: "acceptance_verifier", Type: "acceptance_verifier", Tiers: lightPlus},
		},
		Edges: []schemas.PipelineEdge{
			{From: "code_writer", To: "test_generator", Payload: schemas.EdgePayloadSummary},
			{From: "code_writer", To: "static_analyzer", Payload: schemas.EdgePayloadSummary},
			{From: "test_generator", To: "security_auditor", Payload: schemas.EdgePayloadSummary},
			// The audit follows both the write and the static analysis. The
			// auditor is model-free today and reads neither summary. The edges
			// are here because the hybrid security advisor will read the
			// written-code and analysis summaries, and edge scoping delivers a
			// summary only across a declared edge. This edge precedes the
			// analyzer-to-runner edge so the topological order stays stable. This
			// edge also serializes the analyzer and the auditor in the default
			// graph; RR13 (parallel analyzers) would need a different shape.
			{From: "code_writer", To: "security_auditor", Payload: schemas.EdgePayloadSummary},
			{From: "static_analyzer", To: "security_auditor", Payload: schemas.EdgePayloadSummary},
			{From: "static_analyzer", To: "test_runner", Payload: schemas.EdgePayloadSummary},
			{From: "test_runner", To: "acceptance_verifier", Payload: schemas.EdgePayloadSummary},
		},
	}
}

// DefaultTopology returns a copy of the embedded default pipeline. The CLI
// exports and shows it under the name "default".
func DefaultTopology() *schemas.PipelineTopology {
	return defaultTopology()
}

// CompileTopology resolves one topology into the ordered stage list and the
// token budget for a tier. It filters inactive nodes, enforces the
// compile-time rules, orders the graph, and resolves every node budget.
func CompileTopology(topology *schemas.PipelineTopology, tier schemas.PipelineTier) (CompiledTopology, error) {
	if topology == nil {
		return CompiledTopology{}, fmt.Errorf("topology is nil")
	}
	if err := topology.Validate(); err != nil {
		return CompiledTopology{}, err
	}
	if err := tier.Validate(); err != nil {
		return CompiledTopology{}, err
	}

	active := make([]schemas.PipelineNode, 0, len(topology.Nodes))
	activeByName := make(map[string]schemas.PipelineNode, len(topology.Nodes))
	for _, node := range topology.Nodes {
		if !node.ActiveAtTier(tier) {
			continue
		}
		active = append(active, node)
		activeByName[node.Name] = node
	}
	if len(active) == 0 {
		return CompiledTopology{}, fmt.Errorf("tier %s: topology %s: no nodes are active at tier %q", tier, topology.Name, tier)
	}

	activeEdges := make([]schemas.PipelineEdge, 0, len(topology.Edges))
	for _, edge := range topology.Edges {
		_, fromActive := activeByName[edge.From]
		_, toActive := activeByName[edge.To]
		switch {
		case fromActive && toActive:
			activeEdges = append(activeEdges, edge)
		case toActive && !fromActive:
			return CompiledTopology{}, fmt.Errorf("topology %s: node %s is active at tier %q but depends on %s, which is filtered out", topology.Name, edge.To, tier, edge.From)
		}
	}

	ordered, err := topologicalOrderNodes(active, activeEdges)
	if err != nil {
		return CompiledTopology{}, fmt.Errorf("topology %s: %w", topology.Name, err)
	}

	dependencies := make(map[string][]string, len(active))
	edgePayloads := make(map[string]map[string]schemas.EdgePayload, len(active))
	for _, edge := range activeEdges {
		dependencies[edge.To] = append(dependencies[edge.To], edge.From)
		// Summary is the edge default, so only a non-default payload is
		// recorded. A topology whose edges are all summary keeps an empty
		// field and its serialized form is unchanged.
		payload := edge.Payload.Effective()
		if payload == schemas.EdgePayloadSummary {
			continue
		}
		if edgePayloads[edge.To] == nil {
			edgePayloads[edge.To] = make(map[string]schemas.EdgePayload, 1)
		}
		edgePayloads[edge.To][edge.From] = payload
	}

	baseBudgets := stageBudgets(tier)
	tierBudget, err := BudgetForTier(tier)
	if err != nil {
		return CompiledTopology{}, err
	}
	perStage := make(map[string]schemas.StageBudget, len(ordered))
	stages := make([]schemas.ExecutionStage, 0, len(ordered))
	var sumInput, sumOutput, agentInput, agentOutput int
	for _, node := range ordered {
		budget, err := resolveNodeBudget(node, baseBudgets)
		if err != nil {
			return CompiledTopology{}, fmt.Errorf("topology %s: %w", topology.Name, err)
		}
		perStage[node.Name] = budget
		if node.Type == schemas.NodeTypeAgent {
			// An agent node's explicit budget is its own hard cap. It is added on
			// top of the tier envelope, so it does not count against the envelope.
			agentInput += budget.InputMax
			agentOutput += budget.OutputMax
		} else {
			sumInput += budget.InputMax
			sumOutput += budget.OutputMax
		}
		stages = append(stages, schemas.ExecutionStage{
			Name:         node.Name,
			Type:         node.Type,
			Budget:       budget,
			DependsOn:    append([]string(nil), dependencies[node.Name]...),
			EdgePayloads: edgePayloads[node.Name],
			Caps:         resolvedNodeCapsPtr(node),
			Model:        cloneNodeModel(node.Model),
		})
	}

	totalInput := tierBudget.TotalInputBudget
	totalOutput := tierBudget.TotalOutputBudget
	if topology.Budget != nil {
		totalInput = topology.Budget.TotalInput
		totalOutput = topology.Budget.TotalOutput
	}
	if sumInput > totalInput {
		return CompiledTopology{}, fmt.Errorf("tier %s: topology %s: node budgets total %d input tokens, over the %d input envelope for this tier. Set \"tiers\" on the nodes that run only at larger tiers, shrink a node \"budget\", or raise budget.total_input", tier, topology.Name, sumInput, totalInput)
	}
	if sumOutput > totalOutput {
		return CompiledTopology{}, fmt.Errorf("tier %s: topology %s: node budgets total %d output tokens, over the %d output envelope for this tier. Set \"tiers\" on the nodes that run only at larger tiers, shrink a node \"budget\", or raise budget.total_output", tier, topology.Name, sumOutput, totalOutput)
	}

	return CompiledTopology{
		Name:   topology.Name,
		Tier:   tier,
		Stages: stages,
		Budget: schemas.TokenBudget{
			// The run-level totals include the agent budgets, so the trajectory
			// rule counts agent spend against the same allowance.
			TotalInputBudget:  totalInput + agentInput,
			TotalOutputBudget: totalOutput + agentOutput,
			PerStage:          perStage,
			Reserve:           tierBudget.Reserve,
			OverflowPolicy:    tierBudget.OverflowPolicy,
		},
		Warnings:    compileWarnings(active, activeEdges, topology.Nodes),
		TierInput:   totalInput,
		TierOutput:  totalOutput,
		AgentInput:  agentInput,
		AgentOutput: agentOutput,
	}, nil
}

// pipelineTiers lists every pipeline tier. A topology is compiled at each one.
var pipelineTiers = []schemas.PipelineTier{
	schemas.TierTrivial,
	schemas.TierLight,
	schemas.TierStandard,
	schemas.TierSubstantial,
	schemas.TierArchitectural,
}

// ValidateTopologyTiers compiles a topology at every tier. A topology that
// exceeds a tier's token envelope, or leaves a tier with no active node, fails
// here. The errors from CompileTopology name the tier. A run then never spends
// tokens on a plan that cannot compile.
func ValidateTopologyTiers(topology *schemas.PipelineTopology) error {
	for _, tier := range pipelineTiers {
		if _, err := CompileTopology(topology, tier); err != nil {
			return err
		}
	}
	return nil
}

// cloneNodeModel copies a node's model declaration so the compiled plan never
// aliases the topology that produced it.
func cloneNodeModel(model *schemas.StageModelConfig) *schemas.StageModelConfig {
	if model == nil {
		return nil
	}
	cloned := *model
	return &cloned
}

// resolvedNodeCapsPtr resolves a node's capabilities with every pointer field
// filled, so an execution stage always carries a complete capability set.
func resolvedNodeCapsPtr(node schemas.PipelineNode) *schemas.NodeCapabilities {
	caps := node.EffectiveCapabilities()
	pullContext := node.PullsContext()
	pullMemory := node.PullsMemory()
	return &schemas.NodeCapabilities{
		ModelFree:            caps.ModelFree,
		PullContext:          &pullContext,
		PullMemory:           &pullMemory,
		ProducesVerification: caps.ProducesVerification,
	}
}

// resolveNodeBudget returns the effective budget for one node. An explicit
// node budget wins. Builtin nodes derive from the tier budget table, prompt and
// agent nodes derive from the tier test-generator budget, and command nodes are
// zero by construction.
func resolveNodeBudget(node schemas.PipelineNode, base map[string]schemas.StageBudget) (schemas.StageBudget, error) {
	if node.Budget != nil {
		return *node.Budget, nil
	}
	if schemas.IsBuiltinStageType(node.Type) {
		budget, ok := base[node.Type]
		if !ok {
			return schemas.StageBudget{}, fmt.Errorf("node %s: no budget table entry for type %q", node.Name, node.Type)
		}
		return budget, nil
	}
	switch node.Type {
	case schemas.NodeTypePrompt, schemas.NodeTypeAgent:
		budget, ok := base["test_generator"]
		if !ok {
			return schemas.StageBudget{}, fmt.Errorf("node %s: no test_generator budget to derive a %s budget from", node.Name, node.Type)
		}
		return budget, nil
	case schemas.NodeTypeCommand:
		return schemas.StageBudget{}, nil
	default:
		return schemas.StageBudget{}, fmt.Errorf("node %s: cannot derive a budget for type %q", node.Name, node.Type)
	}
}

// topologicalOrderNodes returns the active nodes in dependency order. It uses
// Kahn's algorithm with a FIFO queue seeded in node declaration order, so the
// result is deterministic.
func topologicalOrderNodes(nodes []schemas.PipelineNode, edges []schemas.PipelineEdge) ([]schemas.PipelineNode, error) {
	byName := make(map[string]schemas.PipelineNode, len(nodes))
	inDegree := make(map[string]int, len(nodes))
	dependents := make(map[string][]string, len(nodes))
	for _, node := range nodes {
		byName[node.Name] = node
		inDegree[node.Name] = 0
	}
	for _, edge := range edges {
		if _, ok := byName[edge.From]; !ok {
			return nil, fmt.Errorf("edge %s -> %s: from is not an active node", edge.From, edge.To)
		}
		if _, ok := byName[edge.To]; !ok {
			return nil, fmt.Errorf("edge %s -> %s: to is not an active node", edge.From, edge.To)
		}
		inDegree[edge.To]++
		dependents[edge.From] = append(dependents[edge.From], edge.To)
	}

	ready := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if inDegree[node.Name] == 0 {
			ready = append(ready, node.Name)
		}
	}

	ordered := make([]schemas.PipelineNode, 0, len(nodes))
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		ordered = append(ordered, byName[name])
		for _, dependent := range dependents[name] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	if len(ordered) != len(nodes) {
		unresolved := make([]string, 0)
		for name, degree := range inDegree {
			if degree > 0 {
				unresolved = append(unresolved, name)
			}
		}
		sort.Strings(unresolved)
		return nil, fmt.Errorf("cycle detected among nodes: %s", strings.Join(unresolved, ", "))
	}
	return ordered, nil
}

// noVerificationWarning is the compile warning for a topology with no
// verification node at all. It is the one place that text lives: the run start
// prints it once, and the not-verified reason names the topology from it.
const noVerificationWarning = "topology has no verification node; revision loops are disabled"

// tierNoVerificationWarning is the compile warning for a tier where no
// verification node of the topology is active, while another tier has one.
// The tier, not the topology, is then the cause of an unverified run.
const tierNoVerificationWarning = "no verification node is active at this tier; revision loops are disabled"

// compileWarnings returns the non-fatal compile warnings for a topology. nodes
// and edges are the active set; all is every node of the topology.
func compileWarnings(nodes []schemas.PipelineNode, edges []schemas.PipelineEdge, all []schemas.PipelineNode) []string {
	byName := make(map[string]schemas.PipelineNode, len(nodes))
	for _, node := range nodes {
		byName[node.Name] = node
	}
	var warnings []string
	for _, edge := range edges {
		from, to := byName[edge.From], byName[edge.To]
		fromFree := from.EffectiveCapabilities().ModelFree
		toFree := to.EffectiveCapabilities().ModelFree
		if fromFree && !toFree {
			warnings = append(warnings, fmt.Sprintf("edge %s -> %s carries command output into a model-backed node; the output is bounded, marked as data, and treated as untrusted", edge.From, edge.To))
		}
		if from.Type == schemas.NodeTypeAgent && !toFree {
			warnings = append(warnings, fmt.Sprintf("edge %s -> %s carries agent output into a model-backed node; the output is model text, bounded, marked as data, and treated as untrusted", edge.From, edge.To))
		}
	}

	// test_generator reads the code writer's summary under the builtin key.
	// The key is filled from the node whose type is code_writer, under any
	// name, when that node is a direct upstream dependency that delivers its
	// summary. A renamed writer therefore satisfies the read.
	writerNames := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if node.Type == "code_writer" {
			writerNames[node.Name] = true
		}
	}
	writerUpstream := make(map[string]bool, len(nodes))
	for _, edge := range edges {
		if !writerNames[edge.From] {
			continue
		}
		if edge.Payload.Effective() == schemas.EdgePayloadNone {
			continue
		}
		writerUpstream[edge.To] = true
	}
	for _, node := range nodes {
		if node.Type == "test_generator" && !writerUpstream[node.Name] {
			warnings = append(warnings, fmt.Sprintf("node %s has no code_writer upstream edge; it will run without code-writer context", node.Name))
		}
	}
	// A graph with no verification node single-passes: there is nothing to
	// revise against. The notice is emitted here (load time) and once at run
	// start, never silently.
	verifies := func(set []schemas.PipelineNode) bool {
		for _, node := range set {
			if node.EffectiveCapabilities().ProducesVerification {
				return true
			}
		}
		return false
	}
	switch {
	case !verifies(all):
		warnings = append(warnings, noVerificationWarning)
	case !verifies(nodes):
		warnings = append(warnings, tierNoVerificationWarning)
	}
	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

// FormatTopology renders a topology's nodes, edges, and command nodes. Command
// nodes are always listed, so an executable command is visible before it runs.
// Every line passes the notice sanitizer, so no control character from a
// topology file reaches the terminal.
func FormatTopology(topology *schemas.PipelineTopology) string {
	var b strings.Builder
	line := func(format string, args ...any) {
		b.WriteString(sanitizeNoticeLine(fmt.Sprintf(format, args...)))
		b.WriteString("\n")
	}
	line("topology: %s", topology.Name)
	if topology.Description != "" {
		line("description: %s", topology.Description)
	}
	line("nodes:")
	for _, node := range topology.Nodes {
		caps := node.EffectiveCapabilities()
		kind := "model"
		if caps.ModelFree {
			kind = "model-free"
		}
		tiers := "all tiers"
		if len(node.Tiers) > 0 {
			parts := make([]string, 0, len(node.Tiers))
			for _, tier := range node.Tiers {
				parts = append(parts, string(tier))
			}
			tiers = strings.Join(parts, ",")
		}
		line("  %s (%s, %s, %s)", node.Name, node.Type, kind, tiers)
		if node.Model != nil {
			effort := node.Model.ReasoningEffort
			if effort == "" {
				effort = "default effort"
			}
			line("    model: %s/%s (%s)", node.Model.ProviderProfile, node.Model.Model, effort)
		}
	}
	line("edges:")
	if len(topology.Edges) == 0 {
		line("  (none)")
	}
	for _, edge := range topology.Edges {
		line("  %s -> %s (%s)", edge.From, edge.To, edge.Payload.Effective())
	}
	line("command nodes:")
	commandNodes := 0
	for _, node := range topology.Nodes {
		if node.Type != schemas.NodeTypeCommand {
			continue
		}
		commandNodes++
		line("  %s: %s", node.Name, stages.CommandLine(node.Command))
	}
	if commandNodes == 0 {
		line("  (none)")
	}
	// Agent nodes run a model with tools, so each one is listed with its tools
	// and permission. The section appears only when the topology has one.
	agentNodes := 0
	for _, node := range topology.Nodes {
		if node.Type != schemas.NodeTypeAgent {
			continue
		}
		if agentNodes == 0 {
			line("agent nodes:")
		}
		agentNodes++
		line("  %s", AgentNodeLine(node))
	}
	return b.String()
}
