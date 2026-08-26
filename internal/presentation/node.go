package presentation

import (
	"fmt"
	"math"
	"strings"
)

// NodeKind is the open set of execution node kinds. The known kinds are
// constants; any non-empty uppercase-safe value is valid so future
// topologies need no code change here.
type NodeKind string

const (
	NodeKindWrite    NodeKind = "WRITE"
	NodeKindAnalyze  NodeKind = "ANALYZE"
	NodeKindSecurity NodeKind = "SECURITY"
	NodeKindLint     NodeKind = "LINT"
	NodeKindCustom   NodeKind = "CUSTOM"
	NodeKindTest     NodeKind = "TEST"
	NodeKindVerify   NodeKind = "VERIFY"
	NodeKindReview   NodeKind = "REVIEW"
)

// Validate checks the kind format, not membership: non-empty and
// uppercase-safe (letters, digits, underscores, starting with a letter).
func (k NodeKind) Validate() error {
	if k == "" {
		return fmt.Errorf("node kind is required")
	}
	value := string(k)
	for i, r := range value {
		upper := r >= 'A' && r <= 'Z'
		digit := r >= '0' && r <= '9'
		underscore := r == '_'
		if i == 0 && !upper {
			return fmt.Errorf("node kind %q must start with an uppercase letter", k)
		}
		if !upper && !digit && !underscore {
			return fmt.Errorf("node kind %q must be uppercase-safe (A-Z, 0-9, underscore), got %q", k, r)
		}
	}
	return nil
}

// NodeStatus is the closed set of execution node lifecycle statuses.
type NodeStatus string

const (
	NodeStatusPending  NodeStatus = "pending"
	NodeStatusRunning  NodeStatus = "running"
	NodeStatusComplete NodeStatus = "complete"
	NodeStatusFailed   NodeStatus = "failed"
	NodeStatusDegraded NodeStatus = "degraded"
)

// Validate reports an error for any value outside the closed set.
func (s NodeStatus) Validate() error {
	switch s {
	case NodeStatusPending, NodeStatusRunning, NodeStatusComplete, NodeStatusFailed, NodeStatusDegraded:
		return nil
	}
	return fmt.Errorf("unknown node status %q", s)
}

// CostSummary is the monetary and token cost of one node.
type CostSummary struct {
	USD    float64 `json:"usd"`
	Tokens int64   `json:"tokens"`
}

// Validate checks non-negative, finite values.
func (c CostSummary) Validate() error {
	if math.IsNaN(c.USD) || math.IsInf(c.USD, 0) || c.USD < 0 {
		return fmt.Errorf("cost usd must be finite and non-negative, got %v", c.USD)
	}
	if c.Tokens < 0 {
		return fmt.Errorf("cost tokens must be non-negative, got %d", c.Tokens)
	}
	return nil
}

// TokenUsage is one node's token breakdown.
type TokenUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

// Validate checks non-negative counts.
func (u TokenUsage) Validate() error {
	if u.InputTokens < 0 {
		return fmt.Errorf("usage input_tokens must be non-negative, got %d", u.InputTokens)
	}
	if u.OutputTokens < 0 {
		return fmt.Errorf("usage output_tokens must be non-negative, got %d", u.OutputTokens)
	}
	if u.CachedTokens < 0 {
		return fmt.Errorf("usage cached_tokens must be non-negative, got %d", u.CachedTokens)
	}
	if u.ReasoningTokens < 0 {
		return fmt.Errorf("usage reasoning_tokens must be non-negative, got %d", u.ReasoningTokens)
	}
	return nil
}

// UsageSummary aggregates token and cost usage for a node or the whole run.
type UsageSummary struct {
	InputTokens     int64                 `json:"input_tokens"`
	OutputTokens    int64                 `json:"output_tokens"`
	CachedTokens    int64                 `json:"cached_tokens"`
	ReasoningTokens int64                 `json:"reasoning_tokens"`
	CostUSD         float64               `json:"cost_usd"`
	ByNode          map[string]TokenUsage `json:"by_node,omitempty"`
}

// Validate checks non-negative counts and a finite, non-negative cost.
func (u UsageSummary) Validate() error {
	if u.InputTokens < 0 {
		return fmt.Errorf("usage input_tokens must be non-negative, got %d", u.InputTokens)
	}
	if u.OutputTokens < 0 {
		return fmt.Errorf("usage output_tokens must be non-negative, got %d", u.OutputTokens)
	}
	if u.CachedTokens < 0 {
		return fmt.Errorf("usage cached_tokens must be non-negative, got %d", u.CachedTokens)
	}
	if u.ReasoningTokens < 0 {
		return fmt.Errorf("usage reasoning_tokens must be non-negative, got %d", u.ReasoningTokens)
	}
	if math.IsNaN(u.CostUSD) || math.IsInf(u.CostUSD, 0) || u.CostUSD < 0 {
		return fmt.Errorf("usage cost_usd must be finite and non-negative, got %v", u.CostUSD)
	}
	for nodeID, breakdown := range u.ByNode {
		if strings.TrimSpace(nodeID) == "" {
			return fmt.Errorf("usage by_node has an empty node id")
		}
		if err := breakdown.Validate(); err != nil {
			return fmt.Errorf("usage by_node[%s]: %w", nodeID, err)
		}
	}
	return nil
}

// ExecutionNode is one node of the execution graph. Dependencies reference
// other node IDs; the reducer preserves append order, so node ordering is
// stable across applies.
type ExecutionNode struct {
	ID           string       `json:"id"`
	Label        string       `json:"label"`
	Kind         NodeKind     `json:"kind"`
	Status       NodeStatus   `json:"status"`
	Progress     float64      `json:"progress"`
	Iteration    int          `json:"iteration"`
	Cost         CostSummary  `json:"cost"`
	Usage        UsageSummary `json:"usage"`
	Dependencies []string     `json:"dependencies,omitempty"`
}

// Validate checks every field: required identifiers, kind format, closed
// status, in-range progress, non-negative iteration and cost.
func (n ExecutionNode) Validate() error {
	if strings.TrimSpace(n.ID) == "" {
		return fmt.Errorf("node id is required")
	}
	if strings.TrimSpace(n.Label) == "" {
		return fmt.Errorf("node %s: label is required", n.ID)
	}
	if err := n.Kind.Validate(); err != nil {
		return fmt.Errorf("node %s: %w", n.ID, err)
	}
	if err := n.Status.Validate(); err != nil {
		return fmt.Errorf("node %s: %w", n.ID, err)
	}
	if math.IsNaN(n.Progress) || n.Progress < 0 || n.Progress > 1 {
		return fmt.Errorf("node %s: progress must be within [0,1], got %v", n.ID, n.Progress)
	}
	if n.Iteration < 0 {
		return fmt.Errorf("node %s: iteration must be non-negative, got %d", n.ID, n.Iteration)
	}
	if err := n.Cost.Validate(); err != nil {
		return fmt.Errorf("node %s: %w", n.ID, err)
	}
	if err := n.Usage.Validate(); err != nil {
		return fmt.Errorf("node %s: %w", n.ID, err)
	}
	seen := make(map[string]bool, len(n.Dependencies))
	for _, dep := range n.Dependencies {
		if strings.TrimSpace(dep) == "" {
			return fmt.Errorf("node %s: dependency id must not be empty", n.ID)
		}
		if seen[dep] {
			return fmt.Errorf("node %s: duplicate dependency %q", n.ID, dep)
		}
		seen[dep] = true
	}
	return nil
}
