package schemas

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/Taf0711/splice/internal/version"
)

// TopologySchemaVersion is the only pipeline topology version this build
// accepts. Any other value is a hard error that names the supported set.
const TopologySchemaVersion = 1

// Custom pipeline node types. Every other Type value must name a builtin
// stage (see IsBuiltinStageType).
const (
	NodeTypePrompt  = "prompt"
	NodeTypeCommand = "command"
	// NodeTypeAgent is a model-backed node that runs a bounded agent loop over
	// an explicit tool allowlist. It is the only node type with tool access
	// besides the builtin stages.
	NodeTypeAgent = "agent"
)

// Agent node limits. An agent node with no max_turns runs AgentDefaultMaxTurns
// turns. A file with an agent node requires AgentNodeMinSplice or newer.
const (
	AgentDefaultMaxTurns = 8
	AgentMaxTurnsLimit   = 50
	AgentMaxTools        = 16
	AgentNodeMinSplice   = "0.3.0"
	AgentPermissionAsk   = "ask"
	AgentPermissionAuto  = "auto"
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
	// Tools is the tool allowlist of an agent node. It is required on an agent
	// node and must be empty on every other type. Each name must be listed in
	// AgentEligibleTools.
	Tools []string `json:"tools,omitempty"`
	// MaxTurns caps the agent loop turns. Zero means AgentDefaultMaxTurns. It is
	// valid only on an agent node.
	MaxTurns int `json:"max_turns,omitempty"`
	// Permission is the node's own permission mode, "ask" or "auto". Empty
	// means the run's mode. The effective mode is never looser than the run's.
	// It is valid only on an agent node.
	Permission string `json:"permission,omitempty"`
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
// capability flags that drive model-free, context, and memory behavior. Every
// entry must match the stage implementation's own Capabilities() declaration;
// TestBuiltinCapabilityProfileMatchesStageDeclarations fails when one drifts.
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
		PullMemory:           boolPointer(false),
		ProducesVerification: true,
	},
	"security_auditor": {
		ModelFree:            true,
		PullContext:          boolPointer(false),
		PullMemory:           boolPointer(false),
		ProducesVerification: true,
	},
	"test_runner": {
		ModelFree:            true,
		PullContext:          boolPointer(false),
		PullMemory:           boolPointer(false),
		ProducesVerification: true,
	},
	"acceptance_verifier": {
		ModelFree:            true,
		PullContext:          boolPointer(false),
		PullMemory:           boolPointer(false),
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

// hasAgentFields reports whether any agent-only field is set on the node.
func (n PipelineNode) hasAgentFields() bool {
	return len(n.Tools) > 0 || n.MaxTurns != 0 || n.Permission != ""
}

// validateAgentFields checks the agent-only fields of an agent node.
func (n PipelineNode) validateAgentFields() error {
	if len(n.Tools) == 0 || len(n.Tools) > AgentMaxTools {
		return fmt.Errorf("node %s: type %q requires 1 to %d tools, got %d", n.Name, NodeTypeAgent, AgentMaxTools, len(n.Tools))
	}
	seen := make(map[string]bool, len(n.Tools))
	for _, tool := range n.Tools {
		if seen[tool] {
			return fmt.Errorf("node %s: tool %q is listed more than once", n.Name, tool)
		}
		seen[tool] = true
		if !IsAgentEligibleTool(tool) {
			return fmt.Errorf("node %s: tool %q is not allowed on an agent node", n.Name, tool)
		}
	}
	if n.MaxTurns < 0 || n.MaxTurns > AgentMaxTurnsLimit {
		return fmt.Errorf("node %s: max_turns must be 1 to %d, got %d", n.Name, AgentMaxTurnsLimit, n.MaxTurns)
	}
	switch n.Permission {
	case "", AgentPermissionAsk, AgentPermissionAuto:
	default:
		return fmt.Errorf("node %s: permission must be %q or %q, got %q", n.Name, AgentPermissionAsk, AgentPermissionAuto, n.Permission)
	}
	return nil
}

// Validate checks one node against the schema rules.
func (n PipelineNode) Validate() error {
	if strings.TrimSpace(n.Name) == "" {
		return errors.New("node name is required")
	}
	// A node name is shown in every disclosure surface. A control character in it
	// could rewrite the terminal, so it is refused here. The error quotes the
	// name, so the error text stays safe too.
	if hasNonPrintable(n.Name) {
		return fmt.Errorf("node name %q contains a control or non-printable character", n.Name)
	}
	if n.Type != NodeTypeAgent && n.hasAgentFields() {
		return fmt.Errorf("node %s: tools, max_turns, and permission are only valid for type %q", n.Name, NodeTypeAgent)
	}
	switch {
	case n.Type == NodeTypeAgent:
		if strings.TrimSpace(n.Prompt) == "" {
			return fmt.Errorf("node %s: type %q requires a non-empty prompt", n.Name, NodeTypeAgent)
		}
		if len(n.Command) > 0 {
			return fmt.Errorf("node %s: command is only valid for type %q", n.Name, NodeTypeCommand)
		}
		if n.Caps.ModelFree {
			return fmt.Errorf("node %s: type %q is model-backed; model_free must be false", n.Name, NodeTypeAgent)
		}
		if n.Caps.ProducesVerification {
			return fmt.Errorf("node %s: type %q cannot set produces_verification; verification comes from builtin verification stages", n.Name, NodeTypeAgent)
		}
		if err := n.validateAgentFields(); err != nil {
			return err
		}
		if n.Budget == nil {
			return fmt.Errorf("node %s: type %q requires an explicit budget; set budget.input_max and budget.output_max", n.Name, NodeTypeAgent)
		}
	case IsBuiltinStageType(n.Type):
		if n.Prompt != "" {
			return fmt.Errorf("node %s: prompt is only valid for type %q", n.Name, NodeTypePrompt)
		}
		if len(n.Command) > 0 {
			return fmt.Errorf("node %s: command is only valid for type %q", n.Name, NodeTypeCommand)
		}
		if profile, ok := builtinCapabilityProfile[n.Type]; ok && !profile.ModelFree && n.Caps.ModelFree {
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
		if n.Caps.ProducesVerification {
			return fmt.Errorf("node %s: type %q cannot set produces_verification; verification comes from builtin verification stages", n.Name, NodeTypePrompt)
		}
	case n.Type == NodeTypeCommand:
		if len(n.Command) == 0 {
			return fmt.Errorf("node %s: type %q requires a non-empty command", n.Name, NodeTypeCommand)
		}
		if n.Prompt != "" {
			return fmt.Errorf("node %s: prompt is only valid for type %q", n.Name, NodeTypePrompt)
		}
		if n.Caps.ProducesVerification {
			return fmt.Errorf("node %s: type %q cannot set produces_verification; verification comes from builtin verification stages", n.Name, NodeTypeCommand)
		}
	default:
		return fmt.Errorf("node %s: unknown node type %q (want a builtin stage name, %q, %q, or %q)", n.Name, n.Type, NodeTypePrompt, NodeTypeCommand, NodeTypeAgent)
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
	if n.Model != nil && effective.ModelFree {
		return fmt.Errorf("node %s: type %q is model-free; a model declaration is not valid", n.Name, n.Type)
	}
	// A capability that needs a model prompt is a contradiction on a model-free
	// node: nothing consumes it, and pull_memory would still pay for a memory
	// retrieval. Reject it rather than let the declaration be silently ignored.
	if effective.ModelFree {
		if n.Caps.PullContext != nil && *n.Caps.PullContext {
			return fmt.Errorf("node %s: type %q is model-free; pull_context has no effect", n.Name, n.Type)
		}
		if n.Caps.PullMemory != nil && *n.Caps.PullMemory {
			return fmt.Errorf("node %s: type %q is model-free; pull_memory has no effect", n.Name, n.Type)
		}
	}
	// A prompt node renders intent, summaries, and memory. It has no context
	// variable in v1, so an explicit pull_context there is inert too. Its
	// pull_memory is meaningful ({{memory}}).
	if (n.Type == NodeTypePrompt || n.Type == NodeTypeAgent) && n.Caps.PullContext != nil && *n.Caps.PullContext {
		return fmt.Errorf("node %s: type %q has no context variable in v1; pull_context has no effect", n.Name, n.Type)
	}
	if n.Model != nil {
		if err := n.Model.Validate(); err != nil {
			return fmt.Errorf("node %s: model: %w", n.Name, err)
		}
		if err := printableField("model.provider_profile", n.Model.ProviderProfile); err != nil {
			return fmt.Errorf("node %s: %w", n.Name, err)
		}
		if err := printableField("model.model", n.Model.Model); err != nil {
			return fmt.Errorf("node %s: %w", n.Name, err)
		}
	}
	return nil
}

// hasNonPrintable reports whether text has a control or non-printable
// character.
func hasNonPrintable(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0
}

// printableField refuses a topology value that a disclosure surface prints
// when it has a control or non-printable character, with the same rule as a
// node name. The error quotes the value, so the error text stays safe too.
func printableField(field, value string) error {
	if hasNonPrintable(value) {
		return fmt.Errorf("%s %q contains a control or non-printable character", field, value)
	}
	return nil
}

// topologyNamePattern is the one rule for a topology name. The user library
// stores each topology as <name>.json, so the name must be a safe file name.
var topologyNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidateTopologyName reports whether name is a valid topology name: 1 to 64
// lowercase letters, digits, "-", or "_", that start with a letter or digit.
func ValidateTopologyName(name string) error {
	if !topologyNamePattern.MatchString(name) {
		return fmt.Errorf("invalid pipeline name %q: use 1 to 64 lowercase letters, digits, '-', or '_', and start with a letter or digit", name)
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
	if err := ValidateTopologyName(t.Name); err != nil {
		return err
	}
	if err := printableField("description", t.Description); err != nil {
		return fmt.Errorf("topology %s: %w", t.Name, err)
	}
	if err := printableField("author", t.Author); err != nil {
		return fmt.Errorf("topology %s: %w", t.Name, err)
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
	if err := t.validateAgentMinSplice(); err != nil {
		return err
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

// validateAgentMinSplice requires splice_min_version to name AgentNodeMinSplice
// or newer when the topology has an agent node. An older build cannot run the
// node, so the file must say so instead of failing at run time.
func (t PipelineTopology) validateAgentMinSplice() error {
	hasAgent := false
	for _, node := range t.Nodes {
		if node.Type == NodeTypeAgent {
			hasAgent = true
			break
		}
	}
	if !hasAgent {
		return nil
	}
	need := fmt.Sprintf("topology %s has agent nodes and needs splice_min_version %s or newer", t.Name, AgentNodeMinSplice)
	if strings.TrimSpace(t.MinSplice) == "" {
		return fmt.Errorf("%s; set splice_min_version", need)
	}
	// SatisfiesMin fails loud on a malformed minimum. The current argument is a
	// fixed release, so the call only checks the declared value.
	if _, err := version.SatisfiesMin("0.0.0", t.MinSplice); err != nil {
		return fmt.Errorf("%s: %w", need, err)
	}
	if ok, _ := version.SatisfiesMin(t.MinSplice, AgentNodeMinSplice); !ok {
		return fmt.Errorf("%s, got %s", need, t.MinSplice)
	}
	return nil
}

// ValidateMinSplice reports an error when this build is older than the
// topology's declared minimum. An empty requirement passes. A build version
// that is not a release version, such as "dev", skips the check, so local work
// is not blocked. A malformed requirement fails loud.
func (t PipelineTopology) ValidateMinSplice(current string) error {
	ok, err := version.SatisfiesMin(current, t.MinSplice)
	if err != nil {
		return fmt.Errorf("topology %s: splice_min_version: %w", t.Name, err)
	}
	if !ok {
		return fmt.Errorf("topology %s requires Splice %s or newer; this build is %s", t.Name, t.MinSplice, current)
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
