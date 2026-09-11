package splice

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// CompiledTopology is the executable shape of one topology at one tier.
type CompiledTopology struct {
	Name     string
	Tier     schemas.PipelineTier
	Stages   []schemas.ExecutionStage
	Budget   schemas.TokenBudget
	Warnings []string
}

// defaultTopology returns the embedded default pipeline as a topology. The
// graph is the compiled form of today's stage roster. The edges follow one
// rule: a node is active at every tier where its dependents are active, so
// tier filtering never leaves a dangling dependency.
func defaultTopology() *schemas.PipelineTopology {
	standardPlus := []schemas.PipelineTier{schemas.TierStandard, schemas.TierSubstantial, schemas.TierArchitectural}
	lightPlus := []schemas.PipelineTier{schemas.TierLight, schemas.TierStandard, schemas.TierSubstantial, schemas.TierArchitectural}
	substantialPlus := []schemas.PipelineTier{schemas.TierSubstantial, schemas.TierArchitectural}
	return &schemas.PipelineTopology{
		Version:     schemas.TopologySchemaVersion,
		Name:        "default",
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
			{From: "static_analyzer", To: "test_runner", Payload: schemas.EdgePayloadSummary},
			{From: "test_runner", To: "acceptance_verifier", Payload: schemas.EdgePayloadSummary},
		},
	}
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
		return CompiledTopology{}, fmt.Errorf("topology %s: no nodes are active at tier %q", topology.Name, tier)
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
	for _, edge := range activeEdges {
		dependencies[edge.To] = append(dependencies[edge.To], edge.From)
	}

	baseBudgets := stageBudgets(tier)
	tierBudget, err := BudgetForTier(tier)
	if err != nil {
		return CompiledTopology{}, err
	}
	perStage := make(map[string]schemas.StageBudget, len(ordered))
	stages := make([]schemas.ExecutionStage, 0, len(ordered))
	var sumInput, sumOutput int
	for _, node := range ordered {
		budget, err := resolveNodeBudget(node, baseBudgets)
		if err != nil {
			return CompiledTopology{}, fmt.Errorf("topology %s: %w", topology.Name, err)
		}
		perStage[node.Name] = budget
		sumInput += budget.InputMax
		sumOutput += budget.OutputMax
		stages = append(stages, schemas.ExecutionStage{
			Name:      node.Name,
			Budget:    budget,
			DependsOn: append([]string(nil), dependencies[node.Name]...),
		})
	}

	totalInput := tierBudget.TotalInputBudget
	totalOutput := tierBudget.TotalOutputBudget
	if topology.Budget != nil {
		totalInput = topology.Budget.TotalInput
		totalOutput = topology.Budget.TotalOutput
	}
	if sumInput > totalInput {
		return CompiledTopology{}, fmt.Errorf("topology %s: node budgets total %d input tokens, over the %d input envelope; trim nodes, shrink budgets, or declare budget.total", topology.Name, sumInput, totalInput)
	}
	if sumOutput > totalOutput {
		return CompiledTopology{}, fmt.Errorf("topology %s: node budgets total %d output tokens, over the %d output envelope; trim nodes, shrink budgets, or declare budget.total", topology.Name, sumOutput, totalOutput)
	}

	return CompiledTopology{
		Name:   topology.Name,
		Tier:   tier,
		Stages: stages,
		Budget: schemas.TokenBudget{
			TotalInputBudget:  totalInput,
			TotalOutputBudget: totalOutput,
			PerStage:          perStage,
			Reserve:           tierBudget.Reserve,
			OverflowPolicy:    tierBudget.OverflowPolicy,
		},
		Warnings: compileWarnings(active, activeEdges),
	}, nil
}

// resolveNodeBudget returns the effective budget for one node. An explicit
// node budget wins. Builtin nodes derive from the tier budget table, prompt
// nodes derive from the tier test-generator budget, and command nodes are
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
	case schemas.NodeTypePrompt:
		budget, ok := base["test_generator"]
		if !ok {
			return schemas.StageBudget{}, fmt.Errorf("node %s: no test_generator budget to derive a prompt budget from", node.Name)
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

// compileWarnings returns the non-fatal compile warnings for a topology.
func compileWarnings(nodes []schemas.PipelineNode, edges []schemas.PipelineEdge) []string {
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
			warnings = append(warnings, fmt.Sprintf("edge %s -> %s carries command output into a model-backed node; the output is bounded, delimited, and treated as untrusted data", edge.From, edge.To))
		}
	}

	hasCodeWriterUpstream := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if node.Type != "code_writer" {
			continue
		}
		for _, dependent := range edges {
			if dependent.From == node.Name {
				hasCodeWriterUpstream[dependent.To] = true
			}
		}
	}
	for _, node := range nodes {
		if node.Type == "test_generator" && !hasCodeWriterUpstream[node.Name] {
			warnings = append(warnings, fmt.Sprintf("node %s has no code_writer upstream edge; it will run without code-writer context", node.Name))
		}
	}
	if len(warnings) == 0 {
		return nil
	}
	return warnings
}
