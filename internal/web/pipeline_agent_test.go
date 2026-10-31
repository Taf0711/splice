package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// agentSecret is a secret value that the agent prompt carries.
const agentSecret = "sk-AGENT-PROMPT-SECRET-77"

// pipelineMixed is a writer, an agent node with a shell tool, and a command
// node after it.
func pipelineMixed() *schemas.PipelineTopology {
	return &schemas.PipelineTopology{
		Version:   schemas.TopologySchemaVersion,
		Name:      "mixed",
		MinSplice: schemas.AgentNodeMinSplice,
		Nodes: []schemas.PipelineNode{
			{Name: "code_writer", Type: "code_writer"},
			{
				Name:       "investigate",
				Type:       schemas.NodeTypeAgent,
				Prompt:     "Find where {{intent}} is handled. <script>alert(1)</script> key " + agentSecret,
				Tools:      []string{"read_file", "grep", "bash"},
				MaxTurns:   3,
				Permission: schemas.AgentPermissionAsk,
				Budget:     &schemas.StageBudget{InputMax: 4000, OutputMax: 1000},
			},
			{Name: "lint", Type: schemas.NodeTypeCommand, Command: []string{"go", "vet", "./..."}},
		},
		Edges: []schemas.PipelineEdge{
			{From: "code_writer", To: "investigate"},
			{From: "investigate", To: "lint"},
		},
	}
}

func mixedWorkspace(t *testing.T) splicerun.TopologySources {
	t.Helper()
	root := t.TempDir()
	data, err := json.Marshal(pipelineMixed())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".splice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".splice", "pipeline.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return splicerun.TopologySources{WorkspaceRoot: root, Trusted: true, UserConfigDir: t.TempDir()}
}

// TestPipelineDisclosesAgentNodes pins that the page discloses each agent
// node as the trust card does (tools and their reach, the permission rule,
// turns, budget, and a bounded prompt preview), as escaped and redacted text.
func TestPipelineDisclosesAgentNodes(t *testing.T) {
	view := NewPipelineView(mixedWorkspace(t), "")
	view.BeginRun(func() ([]string, error) { return []string{agentSecret}, nil })
	s, cookie := startPipeline(t, view)
	page := readBody(t, send(t, http.MethodGet, s.at("/s/pipeline/"), nil, cookie))
	for _, want := range []string{
		"<h2>Agent nodes</h2>",
		"1 agent node runs a model with its listed tools when this topology is active.",
		"up to 4000 input and 1000 output tokens",
		`<span class="command-name">investigate</span>`,
		"read_file, grep, bash (can run commands)",
		"ask: ask, whatever the run mode; ask prompts for every write, command, and network call",
		"up to 3",
		"input 4000, output 1000 tokens",
		"Find where {{intent}} is handled.",
		`<span class="command-name">lint</span> <code>go vet ./...</code>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "<script>alert(1)") {
		t.Error("the agent prompt preview is not escaped")
	}
	if strings.Contains(page, agentSecret) {
		t.Error("the agent prompt preview carries the secret")
	}
	if strings.Contains(page, "node(s)") {
		t.Error("a count says node(s)")
	}
}

// TestPipelineDrawerShowsAgentNodes pins the drawer of an agent node: type,
// tools, permission, turns, budget meters from its explicit budget, and the
// files the completed pass changed.
func TestPipelineDrawerShowsAgentNodes(t *testing.T) {
	view := NewPipelineView(mixedWorkspace(t), "")
	s, cookie := startPipeline(t, view)

	// No run yet: the configured budget is the agent's explicit budget.
	_, static, _ := drawerOf(t, s, cookie, "investigate")
	if fieldValue(static, "type") != schemas.NodeTypeAgent {
		t.Fatalf("type = %q", fieldValue(static, "type"))
	}
	if got := fieldValue(static, "tools"); got != "read_file, grep, bash (can run commands)" {
		t.Errorf("tools = %q", got)
	}
	if got := fieldValue(static, "permission"); got != "ask, whatever the run mode; ask prompts for every write, command, and network call" {
		t.Errorf("permission = %q", got)
	}
	if got := fieldValue(static, "turns"); got != "up to 3" {
		t.Errorf("turns = %q", got)
	}
	if static.Budget == nil || static.Budget.InputMax != 4000 || static.Budget.OutputMax != 1000 || !static.Budget.Configured {
		t.Fatalf("static budget = %+v, want the explicit 4000/1000", static.Budget)
	}

	compiled, err := splicerun.CompileTopology(pipelineMixed(), schemas.TierStandard)
	if err != nil {
		t.Fatal(err)
	}
	plan := agent.PipelinePlanEvent{Dependencies: map[string][]string{}}
	for _, stage := range compiled.Stages {
		plan.Stages = append(plan.Stages, stage.Name)
		if len(stage.DependsOn) > 0 {
			plan.Dependencies[stage.Name] = stage.DependsOn
		}
	}
	var state presentation.State
	for _, event := range []presentation.StreamEventLike{
		presentrun.AdaptPipelinePlan(schemas.ExecutionPlan{RequestIntent: "x", Tier: schemas.TierStandard, TopologyName: "mixed"}, plan),
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "investigate", Status: "running"}),
		presentation.RouteEvent{NodeID: "investigate", Route: presentation.NodeRoute{Model: "m", InputBudget: 4000, OutputBudget: 1000}},
		presentation.UsageEvent{NodeID: "investigate", Usage: presentation.TokenUsage{InputTokens: 3000, OutputTokens: 200}},
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "investigate", Status: "completed", Detail: "found it", ChangedFiles: []string{"a.go", "b.go"}}),
	} {
		if state, err = presentation.Apply(state, event); err != nil {
			t.Fatal(err)
		}
	}
	view.BeginRun(nil)
	view.Observe(state)
	_, live, _ := drawerOf(t, s, cookie, "investigate")
	if live.Budget == nil || *live.Budget != (splicerun.BudgetUse{InputUsed: 3000, InputMax: 4000, OutputUsed: 200, OutputMax: 1000}) {
		t.Fatalf("live budget = %+v", live.Budget)
	}
	if got := fieldValue(live, "changed"); got != "a.go, b.go" {
		t.Fatalf("changed = %q, want the files of the completed pass", got)
	}
	if fieldValue(live, "tools") == "" {
		t.Fatal("the live drawer lost the agent tools")
	}
}
