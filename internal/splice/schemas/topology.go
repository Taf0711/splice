package schemas

import (
	"errors"
	"fmt"
	"strings"
)

// TopologySchemaVersion is the only pipeline topology version this build
// accepts. Any other value is a hard error that names the supported set.
const TopologySchemaVersion = 1

// Custom pipeline node types. Every other Type value must name a builtin
// stage (see IsBuiltinStageType).
const (
	NodeTypePrompt  = "prompt"
	NodeTypeCommand = "command"
)

// EdgePayload is the information contract across one edge. It decides what
// the downstream node may see from the upstream node.
type EdgePayload string

const (
	// EdgePayloadSummary passes the upstream output summary only. It is the
	// default when Payload is empty.
	EdgePayloadSummary EdgePayload = "summary"
	// EdgePayloadOutput passes the summary plus bounded data JSON.
	EdgePayloadOutput EdgePayload = "output"
	// EdgePayloadNone passes nothing. The edge orders execution only.
	EdgePayloadNone EdgePayload = "none"
)

// Validate reports an error for any value outside the closed set. The empty
// value is valid and means EdgePayloadSummary.
func (p EdgePayload) Validate() error {
	switch p {
	case "", EdgePayloadSummary, EdgePayloadOutput, EdgePayloadNone:
		return nil
	default:
		return fmt.Errorf("edge payload must be summary, output, or none, got %q", p)
	}
}

// Effective returns the payload with the default applied.
func (p EdgePayload) Effective() EdgePayload {
	if p == "" {
		return EdgePayloadSummary
	}
	return p
}

// PipelineNode is one stage in a pipeline topology.
type PipelineNode struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Model  *StageModelConfig `json:"model,omitempty"`
	Prompt string            `json:"prompt,omitempty"`
	// Command is required when Type is "command" and must be empty otherwise.
	Command []string `json:"command,omitempty"`
	// Tiers lists the tiers where this node is active. It is empty when the
	// node is active at every tier.
	Tiers []PipelineTier `json:"tiers,omitempty"`
	// Budget is the explicit per-node budget. It is nil when the compiler
	// derives the budget from the builtin budget table.
	Budget *StageBudget     `json:"budget,omitempty"`
	Caps   NodeCapabilities `json:"capabilities,omitempty"`
}

// NodeCapabilities declares per-node behavior. It replaces every name switch
// in the executor.
type NodeCapabilities struct {
	// ModelFree means the node makes no provider call. It must pair with a
	// zero budget.
	ModelFree bool `json:"model_free,omitempty"`
	// PullContext is nil when the builtin profile decides.
	PullContext *bool `json:"pull_context,omitempty"`
	// PullMemory is nil when the builtin profile decides.
	PullMemory *bool `json:"pull_memory,omitempty"`
	// ProducesVerification marks a node whose report feeds the trajectory
	// monitor. Custom nodes may set it. A builtin profile may also set it.
	ProducesVerification bool `json:"produces_verification,omitempty"`
}

// PipelineEdge is one directed connection between two nodes.
type PipelineEdge struct {
	From    string      `json:"from"`
	To      string      `json:"to"`
	Payload EdgePayload `json:"payload,omitempty"`
}

// PipelineTopology is a complete pipeline definition. It is the unit a user
// edits, shares, and imports.
type PipelineTopology struct {
	Version     int             `json:"version"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Author      string          `json:"author,omitempty"`
	MinSplice   string          `json:"splice_min_version,omitempty"`
	Budget      *BudgetOverride `json:"budget,omitempty"`
	Nodes       []PipelineNode  `json:"nodes"`
	Edges       []PipelineEdge  `json:"edges"`
}

// BudgetOverride replaces the tier envelope with an explicit whole-run
// budget.
type BudgetOverride struct {
	TotalInput  int `json:"total_input"`
	TotalOutput int `json:"total_output"`
}

// Validate checks the whole-run budget override.
func (b BudgetOverride) Validate() error {
	if b.TotalInput <= 0 {
		return errors.New("budget.total_input must be > 0")
	}
	if b.TotalOutput <= 0 {
		return errors.New("budget.total_output must be > 0")
	}
	return nil
}

// builtinCapabilityProfile is the explicit capability map keyed by builtin
// stage type. It is the default source for builtin nodes and for the
// capability flags that drive model-free, context, and memory behavior.
var builtinCapabilityProfile = map[string]NodeCapabilities{
	"code_writer": {
		ModelFree:   false,
		PullContext: boolPointer(true),
		PullMemory:  boolPointer(true),
	},
	"test_generator": {
		ModelFree:   false,
		PullContext: boolPointer(true),
		PullMemory:  boolPointer(true),
	},
	"static_analyzer": {
		ModelFree:            true,
		PullContext:          boolPointer(false),
		PullMemory:           boolPointer(true),
		ProducesVerification: true,
	},
	"security_auditor": {
		ModelFree:            true,
		PullContext:          boolPointer(false),
		PullMemory:           boolPointer(true),
		ProducesVerification: true,
	},
	"test_runner": {
		ModelFree:            true,
		PullContext:          boolPointer(false),
		PullMemory:           boolPointer(true),
		ProducesVerification: true,
	},
	"acceptance_verifier": {
		ModelFree:            true,
		PullContext:          boolPointer(false),
		PullMemory:           boolPointer(true),
		ProducesVerification: true,
	},
}

// IsBuiltinStageType reports whether the type names a builtin stage.
func IsBuiltinStageType(nodeType string) bool {
	_, ok := builtinCapabilityProfile[nodeType]
	return ok
}

// BuiltinCapabilities returns the builtin capability profile for a builtin
// stage type. The second return is false for an unknown type.
func BuiltinCapabilities(nodeType string) (NodeCapabilities, bool) {
	caps, ok := builtinCapabilityProfile[nodeType]
	return caps, ok
}

// EffectiveCapabilities resolves the capability flags for this node.
// Builtin nodes start from the profile. A prompt node is model-backed. A
// command node is model-free. Explicit pointer fields override the profile.
// The ModelFree and ProducesVerification bools can only add a capability,
// because false is the unset zero value.
func (n PipelineNode) EffectiveCapabilities() NodeCapabilities {
	var caps NodeCapabilities
	if profile, ok := builtinCapabilityProfile[n.Type]; ok {
		caps = profile
	}
	switch n.Type {
	case NodeTypeCommand:
		caps.ModelFree = true
	case NodeTypePrompt:
		caps.ModelFree = false
	}
	if n.Caps.PullContext != nil {
		caps.PullContext = n.Caps.PullContext
	}
	if n.Caps.PullMemory != nil {
		caps.PullMemory = n.Caps.PullMemory
	}
	if n.Caps.ProducesVerification {
		caps.ProducesVerification = true
	}
	return caps
}

// PullsContext reports whether the node pulls context, with the profile
// default applied.
func (n PipelineNode) PullsContext() bool {
	return boolValue(n.EffectiveCapabilities().PullContext)
}

// PullsMemory reports whether the node pulls memory, with the profile
// default applied.
func (n PipelineNode) PullsMemory() bool {
	return boolValue(n.EffectiveCapabilities().PullMemory)
}

// ActiveAtTier reports whether the node is active at the tier.
func (n PipelineNode) ActiveAtTier(tier PipelineTier) bool {
	if len(n.Tiers) == 0 {
		return true
	}
	for _, t := range n.Tiers {
		if t == tier {
			return true
		}
	}
	return false
}

// Validate checks one node against the schema rules.
func (n PipelineNode) Validate() error {
	if strings.TrimSpace(n.Name) == "" {
		return errors.New("node name is required")
	}
	switch {
	case IsBuiltinStageType(n.Type):
		if n.Prompt != "" {
			return fmt.Errorf("node %s: prompt is only valid for type %q", n.Name, NodeTypePrompt)
		}
		if len(n.Command) > 0 {
			return fmt.Errorf("node %s: command is only valid for type %q", n.Name, NodeTypeCommand)
		}
		if n.Caps.ModelFree {
			return fmt.Errorf("node %s: type %q is model-backed in the builtin profile; model_free must be false", n.Name, n.Type)
		}
	case n.Type == NodeTypePrompt:
		if strings.TrimSpace(n.Prompt) == "" {
			return fmt.Errorf("node %s: type %q requires a non-empty prompt", n.Name, NodeTypePrompt)
		}
		if len(n.Command) > 0 {
			return fmt.Errorf("node %s: command is only valid for type %q", n.Name, NodeTypeCommand)
		}
		if n.Caps.ModelFree {
			return fmt.Errorf("node %s: type %q is model-backed; model_free must be false", n.Name, NodeTypePrompt)
		}
	case n.Type == NodeTypeCommand:
		if len(n.Command) == 0 {
			return fmt.Errorf("node %s: type %q requires a non-empty command", n.Name, NodeTypeCommand)
		}
		if n.Prompt != "" {
			return fmt.Errorf("node %s: prompt is only valid for type %q", n.Name, NodeTypePrompt)
		}
	default:
		return fmt.Errorf("node %s: unknown node type %q (want a builtin stage name, %q, or %q)", n.Name, n.Type, NodeTypePrompt, NodeTypeCommand)
	}
	for i, tier := range n.Tiers {
		if err := tier.Validate(); err != nil {
			return fmt.Errorf("node %s: tiers[%d]: %w", n.Name, i, err)
		}
	}
	effective := n.EffectiveCapabilities()
	if n.Budget != nil {
		if err := n.Budget.Validate(); err != nil {
			return fmt.Errorf("node %s: budget: %w", n.Name, err)
		}
		zero := n.Budget.InputMax == 0 && n.Budget.OutputMax == 0
		if effective.ModelFree && !zero {
			return fmt.Errorf("node %s: model-free node has a non-zero budget; model-free nodes must use a zero budget", n.Name)
		}
		if !effective.ModelFree && zero {
			return fmt.Errorf("node %s: model-backed node has a zero budget; model-backed nodes must use a model-backed budget", n.Name)
		}
	}
	if n.Model != nil {
		if err := n.Model.Validate(); err != nil {
			return fmt.Errorf("node %s: model: %w", n.Name, err)
		}
	}
	return nil
}

// Validate checks the topology against the schema rules. It fails loud and
// names the offending value.
func (t PipelineTopology) Validate() error {
	if t.Version != TopologySchemaVersion {
		return fmt.Errorf("topology version %d is not supported; supported versions: [%d]", t.Version, TopologySchemaVersion)
	}
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("topology name is required")
	}
	if len(t.Nodes) == 0 {
		return fmt.Errorf("topology %s: at least one node is required", t.Name)
	}
	nodeByName := make(map[string]PipelineNode, len(t.Nodes))
	for i, node := range t.Nodes {
		if err := node.Validate(); err != nil {
			return fmt.Errorf("nodes[%d]: %w", i, err)
		}
		if _, exists := nodeByName[node.Name]; exists {
			return fmt.Errorf("nodes[%d]: duplicate node name %q", i, node.Name)
		}
		nodeByName[node.Name] = node
	}
	seenEdge := make(map[string]bool, len(t.Edges))
	for i, edge := range t.Edges {
		if strings.TrimSpace(edge.From) == "" {
			return fmt.Errorf("edges[%d]: from is required", i)
		}
		if strings.TrimSpace(edge.To) == "" {
			return fmt.Errorf("edges[%d]: to is required", i)
		}
		if _, ok := nodeByName[edge.From]; !ok {
			return fmt.Errorf("edges[%d]: from %q is not a node", i, edge.From)
		}
		if _, ok := nodeByName[edge.To]; !ok {
			return fmt.Errorf("edges[%d]: to %q is not a node", i, edge.To)
		}
		if err := edge.Payload.Validate(); err != nil {
			return fmt.Errorf("edges[%d] (%s -> %s): %w", i, edge.From, edge.To, err)
		}
		key := edge.From + "\x00" + edge.To
		if seenEdge[key] {
			return fmt.Errorf("edges[%d]: duplicate edge %s -> %s", i, edge.From, edge.To)
		}
		seenEdge[key] = true
	}
	if err := detectTopologyCycle(t.Nodes, t.Edges); err != nil {
		return fmt.Errorf("topology %s: %w", t.Name, err)
	}
	if t.Budget != nil {
		if err := t.Budget.Validate(); err != nil {
			return fmt.Errorf("topology %s: %w", t.Name, err)
		}
	}
	return nil
}

// detectTopologyCycle reports the first cycle reachable from the node list.
// It uses depth-first search over the edge set in declaration order so the
// error is stable.
func detectTopologyCycle(nodes []PipelineNode, edges []PipelineEdge) error {
	outgoing := make(map[string][]string, len(nodes))
	for _, edge := range edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge.To)
	}
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[string]int, len(nodes))
	var stack []string
	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("cycle detected: %s", strings.Join(append(stack, name), " -> "))
		}
		state[name] = visiting
		stack = append(stack, name)
		for _, next := range outgoing[name] {
			if err := visit(next); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = done
		return nil
	}
	for _, node := range nodes {
		if err := visit(node.Name); err != nil {
			return err
		}
	}
	return nil
}

func boolPointer(value bool) *bool {
	return &value
}

func boolValue(pointer *bool) bool {
	return pointer != nil && *pointer
}
