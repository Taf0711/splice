package presentation

import (
	"strings"
	"testing"
)

func TestLifecycleValidation(t *testing.T) {
	valid := []Lifecycle{"", LifecycleDesign, LifecycleCrystallize, LifecycleApprove, LifecycleExecute, LifecycleRecovery, LifecycleComplete}
	for _, l := range valid {
		if err := l.Validate(); err != nil {
			t.Fatalf("lifecycle %q rejected: %v", l, err)
		}
	}
	for _, l := range []Lifecycle{"bogus", "EXECUTE", "design "} {
		if err := l.Validate(); err == nil {
			t.Fatalf("lifecycle %q accepted", l)
		}
	}
}

func TestNodeKindValidation(t *testing.T) {
	for _, kind := range []NodeKind{NodeKindWrite, NodeKindAnalyze, NodeKindSecurity, NodeKindLint, NodeKindCustom, NodeKindTest, NodeKindVerify, NodeKindReview, NodeKind("FUTURE_TOPO")} {
		if err := kind.Validate(); err != nil {
			t.Fatalf("kind %q rejected: %v", kind, err)
		}
	}
	for _, kind := range []NodeKind{"", "write", "WRITE ", "WRITE-1", "1WRITE", "_WRITE"} {
		if err := kind.Validate(); err == nil {
			t.Fatalf("kind %q accepted", kind)
		}
	}
}

func TestNodeStatusValidation(t *testing.T) {
	for _, s := range []NodeStatus{NodeStatusPending, NodeStatusRunning, NodeStatusComplete, NodeStatusFailed, NodeStatusDegraded} {
		if err := s.Validate(); err != nil {
			t.Fatalf("status %q rejected: %v", s, err)
		}
	}
	for _, s := range []NodeStatus{"", "done", "COMPLETE"} {
		if err := s.Validate(); err == nil {
			t.Fatalf("status %q accepted", s)
		}
	}
}

func TestExecutionNodeValidation(t *testing.T) {
	valid := func() ExecutionNode {
		return ExecutionNode{
			ID:       "code_writer",
			Label:    "code_writer",
			Kind:     NodeKindWrite,
			Status:   NodeStatusRunning,
			Progress: 0.5,
		}
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid node rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*ExecutionNode)
		want   string
	}{
		{"empty id", func(n *ExecutionNode) { n.ID = "" }, "id is required"},
		{"empty label", func(n *ExecutionNode) { n.Label = " " }, "label is required"},
		{"unknown kind", func(n *ExecutionNode) { n.Kind = "write" }, "uppercase"},
		{"unknown status", func(n *ExecutionNode) { n.Status = "done" }, "unknown node status"},
		{"negative progress", func(n *ExecutionNode) { n.Progress = -0.1 }, "progress"},
		{"progress over one", func(n *ExecutionNode) { n.Progress = 1.1 }, "progress"},
		{"nan progress", func(n *ExecutionNode) { n.Progress = nan() }, "progress"},
		{"negative iteration", func(n *ExecutionNode) { n.Iteration = -1 }, "iteration"},
		{"negative cost", func(n *ExecutionNode) { n.Cost.Tokens = -3 }, "cost tokens"},
		{"negative usage tokens", func(n *ExecutionNode) { n.Usage.InputTokens = -1 }, "usage input_tokens"},
		{"empty dependency", func(n *ExecutionNode) { n.Dependencies = []string{" "} }, "dependency id"},
		{"duplicate dependency", func(n *ExecutionNode) { n.Dependencies = []string{"a", "a"} }, "duplicate dependency"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := valid()
			tc.mutate(&node)
			err := node.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestUsageSummaryValidation(t *testing.T) {
	if err := (UsageSummary{InputTokens: 1, OutputTokens: 2, CostUSD: 0.5}).Validate(); err != nil {
		t.Fatalf("valid usage rejected: %v", err)
	}
	cases := []struct {
		name  string
		usage UsageSummary
		want  string
	}{
		{"negative input", UsageSummary{InputTokens: -1}, "input_tokens"},
		{"negative output", UsageSummary{OutputTokens: -1}, "output_tokens"},
		{"negative cached", UsageSummary{CachedTokens: -1}, "cached_tokens"},
		{"negative reasoning", UsageSummary{ReasoningTokens: -1}, "reasoning_tokens"},
		{"negative cost", UsageSummary{CostUSD: -0.01}, "cost_usd"},
		{"nan cost", UsageSummary{CostUSD: nan()}, "cost_usd"},
		{"empty by-node key", UsageSummary{ByNode: map[string]TokenUsage{"": {}}}, "empty node id"},
		{"negative by-node tokens", UsageSummary{ByNode: map[string]TokenUsage{"a": {OutputTokens: -1}}}, "by_node[a]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.usage.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestEvidenceGroupValidation(t *testing.T) {
	valid := func() EvidenceGroup {
		return EvidenceGroup{Label: "checks", Status: EvidenceFailed, Failed: 2, Findings: []string{"TestHello failed"}}
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid group rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*EvidenceGroup)
		want   string
	}{
		{"empty label", func(g *EvidenceGroup) { g.Label = "" }, "label is required"},
		{"unknown status", func(g *EvidenceGroup) { g.Status = "ok" }, "unknown evidence status"},
		{"negative passed", func(g *EvidenceGroup) { g.Passed = -1 }, "passed"},
		{"negative failed", func(g *EvidenceGroup) { g.Failed = -1 }, "failed"},
		{"negative incomplete", func(g *EvidenceGroup) { g.Incomplete = -1 }, "incomplete"},
		{"negative duration", func(g *EvidenceGroup) { g.Duration = -1 }, "duration"},
		{"empty finding", func(g *EvidenceGroup) { g.Findings = []string{" "} }, "findings[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			group := valid()
			tc.mutate(&group)
			err := group.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestInterventionValidation(t *testing.T) {
	valid := func() Intervention {
		return Intervention{
			Kind:         InterventionRetry,
			Reason:       "test suite failed twice",
			TargetNodeID: "code_writer",
			Status:       InterventionProposed,
		}
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid intervention rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Intervention)
		want   string
	}{
		{"unknown kind", func(i *Intervention) { i.Kind = "pause" }, "unknown intervention kind"},
		{"unknown status", func(i *Intervention) { i.Status = "maybe" }, "unknown intervention status"},
		{"empty reason", func(i *Intervention) { i.Reason = " " }, "reason is required"},
		{"empty target", func(i *Intervention) { i.TargetNodeID = "" }, "target node id is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			intervention := valid()
			tc.mutate(&intervention)
			err := intervention.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestStateValidation(t *testing.T) {
	valid := func() State {
		return State{
			SchemaVersion: PresentationSchemaVersionV1,
			Lifecycle:     LifecycleExecute,
			Plan:          Plan{Title: "hello", TaskCount: 2},
			Nodes: []ExecutionNode{
				{ID: "code_writer", Label: "code_writer", Kind: NodeKindWrite, Status: NodeStatusComplete, Progress: 1},
				{ID: "test_runner", Label: "test_runner", Kind: NodeKindTest, Status: NodeStatusFailed, Progress: 0.5},
			},
		}
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid state rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*State)
		want   string
	}{
		{"wrong schema version", func(s *State) { s.SchemaVersion = 2 }, "schema_version"},
		{"unknown lifecycle", func(s *State) { s.Lifecycle = "bogus" }, "unknown lifecycle"},
		{"negative plan task count", func(s *State) { s.Plan.TaskCount = -1 }, "task_count"},
		{"invalid node", func(s *State) { s.Nodes[0].Progress = 2 }, "progress"},
		{"duplicate node id", func(s *State) { s.Nodes[1].ID = "code_writer" }, "duplicate node id"},
		{"invalid intervention", func(s *State) {
			s.Interventions = []Intervention{{Kind: "pause", Reason: "r", TargetNodeID: "x", Status: InterventionProposed}}
		}, "unknown intervention kind"},
		{"invalid evidence", func(s *State) {
			s.Evidence = []EvidenceGroup{{Label: "checks", Status: "ok"}}
		}, "unknown evidence status"},
		{"invalid trajectory score", func(s *State) { s.Trajectory.PassScores = []float64{1.5} }, "pass_scores"},
		{"invalid file change", func(s *State) {
			s.Files = []FileChangeSummary{{Path: "a.go", Status: "", Additions: 1}}
		}, "status is required"},
		{"invalid usage", func(s *State) { s.Usage.InputTokens = -1 }, "input_tokens"},
		{"invalid completion", func(s *State) {
			s.Completion = &CompletionReceipt{Status: "done"}
		}, "completion status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := valid()
			tc.mutate(&state)
			err := state.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func nan() float64 {
	var zero float64
	return zero / zero
}
