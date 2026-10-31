package splice

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// TestRegisterTopologyNodesAddsCommandNode pins that a command node in the
// active topology reaches the registry keyed by node name.
func TestRegisterTopologyNodesAddsCommandNode(t *testing.T) {
	registry := stageRegistry{}
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "custom",
		Nodes: []schemas.PipelineNode{
			{Name: "lint_cmd", Type: schemas.NodeTypeCommand, Command: []string{"true"}},
		},
	}
	if err := registerTopologyNodes(registry, topology); err != nil {
		t.Fatalf("registerTopologyNodes: %v", err)
	}
	stage, ok := registry["lint_cmd"]
	if !ok {
		t.Fatal("command node not registered under its node name")
	}
	if _, ok := stage.(stages.CommandStage); !ok {
		t.Fatalf("registered %T, want stages.CommandStage", stage)
	}
	if !stage.Capabilities().ModelFree {
		t.Fatal("a command node must be model-free")
	}
}

// TestRegisterTopologyNodesAddsPromptNode pins that a prompt node reaches the
// registry keyed by node name with its template.
func TestRegisterTopologyNodesAddsPromptNode(t *testing.T) {
	registry := stageRegistry{}
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "custom",
		Nodes:   []schemas.PipelineNode{{Name: "note", Type: schemas.NodeTypePrompt, Prompt: "answer {{intent}}"}},
	}
	if err := registerTopologyNodes(registry, topology); err != nil {
		t.Fatalf("registerTopologyNodes: %v", err)
	}
	stage, ok := registry["note"]
	if !ok {
		t.Fatal("prompt node not registered under its node name")
	}
	prompt, ok := stage.(stages.PromptStage)
	if !ok {
		t.Fatalf("registered %T, want stages.PromptStage", stage)
	}
	if prompt.Template != "answer {{intent}}" {
		t.Fatalf("template = %q, want the node template", prompt.Template)
	}
	if prompt.Capabilities().ModelFree {
		t.Fatal("a prompt node is model-backed")
	}
}

// TestRegisterTopologyNodesAliasesRenamedBuiltin pins that a builtin node
// under a different name is still reachable by its node name.
func TestRegisterTopologyNodesAliasesRenamedBuiltin(t *testing.T) {
	registry := stageRegistry{"code_writer": stages.CodeWriter{}}
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "renamed",
		Nodes: []schemas.PipelineNode{
			{Name: "writer", Type: "code_writer"},
		},
	}
	if err := registerTopologyNodes(registry, topology); err != nil {
		t.Fatalf("registerTopologyNodes: %v", err)
	}
	if _, ok := registry["writer"]; !ok {
		t.Fatal("renamed builtin not registered under its node name")
	}
}

// TestRegisterTopologyNodesRejectsUnknownBuiltin pins the fail-loud case for
// a builtin type with no implementation.
func TestRegisterTopologyNodesRejectsUnknownBuiltin(t *testing.T) {
	registry := stageRegistry{}
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "renamed",
		Nodes:   []schemas.PipelineNode{{Name: "ghost", Type: "static_analyzer"}},
	}
	err := registerTopologyNodes(registry, topology)
	if err == nil || !strings.Contains(err.Error(), "no builtin stage") {
		t.Fatalf("error = %v, want the missing-builtin refusal", err)
	}
}

// summarizeRecords renders stage names, statuses, and summaries for a test
// failure message. It keeps the named reason visible without a pointer dump.
func summarizeRecords(records []schemas.StageRecord) string {
	parts := make([]string, 0, len(records))
	for _, record := range records {
		parts = append(parts, fmt.Sprintf("%s/%s: %s", record.Name, record.Status, DerefString(record.OutputSummary)))
	}
	return strings.Join(parts, " | ")
}

// TestRunExecutesWorkspaceTopologyCommandNode is the end-to-end proof for the
// registry seam. The tests above cover registerTopologyNodes in isolation;
// this one proves that Run passes the ACTIVE topology to that helper. A
// trusted workspace topology declares one command node, Run loads and compiles
// it, and the executor runs the command instead of failing with "no configured
// agent".
func TestRunExecutesWorkspaceTopologyCommandNode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workDir, registry := newRunTestWorkspace(t)
	writeWorkspaceTopology(t, workDir, "command-node", schemas.PipelineNode{
		Name: "echo_check", Type: schemas.NodeTypeCommand, Command: []string{"echo", "topology-ran"},
	})

	result, err := Run(context.Background(), "check the project", runFakeProvider{}, agent.Options{
		Cwd:              workDir,
		Registry:         registry,
		PermissionMode:   agent.PermissionModeAuto,
		FileTracker:      tools.NewFileTracker(),
		TrustedWorkspace: true,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var pipeline schemas.PipelineResult
	if err := json.Unmarshal([]byte(result.FinalAnswer), &pipeline); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if pipeline.TopologyName != "command-node" {
		t.Fatalf("topology = %q, want command-node", pipeline.TopologyName)
	}
	if pipeline.Status != "completed" {
		t.Fatalf("pipeline status = %q, want completed; stages = %s", pipeline.Status, summarizeRecords(pipeline.Stages))
	}
	if len(pipeline.Stages) != 1 || pipeline.Stages[0].Name != "echo_check" {
		t.Fatalf("stages = %s, want one echo_check record", summarizeRecords(pipeline.Stages))
	}
	record := pipeline.Stages[0]
	if record.Status != schemas.StageCompleted {
		t.Fatalf("stage status = %q, want completed (%s)", record.Status, DerefString(record.OutputSummary))
	}
	if summary := DerefString(record.OutputSummary); !strings.Contains(summary, "succeeded") {
		t.Fatalf("stage summary = %q, want the command result", summary)
	}
}

// TestRunFailsPassOnFailingCommandNode pins that a failing command node cannot
// yield a verified run, even when the rest of the topology is clean.
func TestRunFailsPassOnFailingCommandNode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workDir, registry := newRunTestWorkspace(t)
	writeWorkspaceTopology(t, workDir, "failing-command", schemas.PipelineNode{
		Name: "lint_cmd", Type: schemas.NodeTypeCommand, Command: []string{"false"},
	})

	result, err := Run(context.Background(), "check the project", runFakeProvider{}, agent.Options{
		Cwd:              workDir,
		Registry:         registry,
		PermissionMode:   agent.PermissionModeAuto,
		FileTracker:      tools.NewFileTracker(),
		TrustedWorkspace: true,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var pipeline schemas.PipelineResult
	if err := json.Unmarshal([]byte(result.FinalAnswer), &pipeline); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if pipeline.Status == "completed" || pipeline.Verified {
		t.Fatalf("pipeline status = %q verified = %v, want a non-verified run; stages = %s", pipeline.Status, pipeline.Verified, summarizeRecords(pipeline.Stages))
	}
	if len(pipeline.Stages) == 0 {
		t.Fatal("stages empty, want the failed lint_cmd record")
	}
	for _, record := range pipeline.Stages {
		if record.Name != "lint_cmd" || record.Status != schemas.StageFailed {
			t.Fatalf("stages = %s, want only failed lint_cmd records", summarizeRecords(pipeline.Stages))
		}
		if summary := DerefString(record.OutputSummary); !strings.Contains(summary, "lint_cmd") {
			t.Fatalf("stage summary = %q, want the node name", summary)
		}
	}
}

// emptyAnswerProvider streams usage and no text, so a prompt node's model call
// succeeds but returns an empty answer.
type emptyAnswerProvider struct{}

func (emptyAnswerProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	ch := make(chan zeroruntime.StreamEvent, 2)
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 10, OutputTokens: 5}}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

// TestRunPromptNodeEmptyAnswerNamesNode pins that a prompt node that returns
// nothing fails the run with the node name and the real cause, not a request
// ledger mismatch. The node's usage must reach the ledger.
func TestRunPromptNodeEmptyAnswerNamesNode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workDir, registry := newRunTestWorkspace(t)
	// An explicit small budget keeps the node inside every tier envelope. A
	// prompt node with no budget takes the test_generator budget (3000 input),
	// which alone exceeds the trivial envelope.
	writeWorkspaceTopology(t, workDir, "prompt-node", schemas.PipelineNode{
		Name: "summarize", Type: schemas.NodeTypePrompt, Prompt: "Summarize: {{intent}}",
		Budget: &schemas.StageBudget{InputMax: 500, OutputMax: 500},
	})

	result, err := Run(context.Background(), "check the project", emptyAnswerProvider{}, agent.Options{
		Cwd:              workDir,
		Registry:         registry,
		PermissionMode:   agent.PermissionModeAuto,
		FileTracker:      tools.NewFileTracker(),
		TrustedWorkspace: true,
	}, nil, nil)
	if err != nil && strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Run error = %v, want the prompt node failure, not a ledger mismatch", err)
	}
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var pipeline schemas.PipelineResult
	if err := json.Unmarshal([]byte(result.FinalAnswer), &pipeline); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if pipeline.Status == "completed" {
		t.Fatalf("pipeline status = completed, want the prompt node to fail; stages = %s", summarizeRecords(pipeline.Stages))
	}
	if len(pipeline.Stages) == 0 {
		t.Fatal("no stage records, want the failed summarize record")
	}
	summary := DerefString(pipeline.Stages[len(pipeline.Stages)-1].OutputSummary)
	if !strings.Contains(summary, "prompt node summarize") || !strings.Contains(summary, "model returned no output") {
		t.Fatalf("stage summary = %q, want the prompt node name and its real cause", summary)
	}
}

// TestRunNamesCommandNodesBeforeExecution pins that a run with command nodes
// emits one notice per node through OnNotice, before any stage runs.
func TestRunNamesCommandNodesBeforeExecution(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workDir, registry := newRunTestWorkspace(t)
	writeWorkspaceTopology(t, workDir, "named-commands",
		schemas.PipelineNode{Name: "lint", Type: schemas.NodeTypeCommand, Command: []string{"go", "vet", "./..."}},
		schemas.PipelineNode{Name: "echo_check", Type: schemas.NodeTypeCommand, Command: []string{"echo", "topology-ran"}},
	)
	var notices, reasoning []string
	_, err := Run(context.Background(), "check the project", runFakeProvider{}, agent.Options{
		Cwd:              workDir,
		Registry:         registry,
		PermissionMode:   agent.PermissionModeAuto,
		FileTracker:      tools.NewFileTracker(),
		TrustedWorkspace: true,
		OnNotice:         func(text string) { notices = append(notices, text) },
		OnReasoning:      func(text string) { reasoning = append(reasoning, text) },
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{
		"[topology] command node lint will run: go vet ./...",
		"[topology] command node echo_check will run: echo topology-ran",
	} {
		if !containsString(notices, want) {
			t.Fatalf("notices = %q, want %q", notices, want)
		}
	}
	for _, line := range reasoning {
		if strings.Contains(line, "will run:") {
			t.Fatalf("command notice reached the reasoning stream: %q", line)
		}
	}
}

// TestSanitizeNoticeLine pins that a topology value cannot forge a line break
// or flood the transcript.
func TestSanitizeNoticeLine(t *testing.T) {
	got := sanitizeNoticeLine("[topology] command node x will run: echo a\n\x1b[2Jb\tc")
	if strings.ContainsAny(got, "\n\x1b\t") {
		t.Fatalf("sanitized = %q, want no control characters", got)
	}
	long := sanitizeNoticeLine(strings.Repeat("y", maxNoticeChars*2))
	if n := len([]rune(long)); n > maxNoticeChars+3 {
		t.Fatalf("sanitized length = %d, want at most %d", n, maxNoticeChars+3)
	}
}

// TestRunSendsTopologyWarningsThroughNotices pins that a --pipeline override of
// a trusted project topology reaches OnNotice, not the reasoning stream. The
// source line and the override warning both go to the notice path.
func TestRunSendsTopologyWarningsThroughNotices(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workDir, registry := newRunTestWorkspace(t)
	writeWorkspaceTopology(t, workDir, "project-topology",
		schemas.PipelineNode{Name: "echo_check", Type: schemas.NodeTypeCommand, Command: []string{"echo", "project"}},
	)
	overrideDir := t.TempDir()
	writeWorkspaceTopology(t, overrideDir, "override-topology",
		schemas.PipelineNode{Name: "echo_check", Type: schemas.NodeTypeCommand, Command: []string{"echo", "override"}},
	)
	overridePath := filepath.Join(overrideDir, ".splice", "pipeline.json")
	var notices, reasoning []string
	_, err := Run(context.Background(), "check the project", runFakeProvider{}, agent.Options{
		Cwd:              workDir,
		Registry:         registry,
		PermissionMode:   agent.PermissionModeAuto,
		FileTracker:      tools.NewFileTracker(),
		TrustedWorkspace: true,
		Pipeline:         overridePath,
		OnNotice:         func(text string) { notices = append(notices, text) },
		OnReasoning:      func(text string) { reasoning = append(reasoning, text) },
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var override, source bool
	for _, line := range notices {
		override = override || (strings.HasPrefix(line, "[topology] warning: --pipeline ") && strings.Contains(line, "overrides the trusted project topology"))
		source = source || line == "[topology] active pipeline from --pipeline"
	}
	if !override || !source {
		t.Fatalf("notices = %q, want the override warning and the active pipeline line", notices)
	}
	for _, line := range reasoning {
		if strings.Contains(line, "[topology]") {
			t.Fatalf("topology line reached the reasoning stream: %q", line)
		}
	}
}

// TestRunNoVerificationTopologyNoticeOnceAndNamed pins that a custom topology
// with no verification node prints its warning once and is not verified for
// that topology's cause, not the tier.
func TestRunNoVerificationTopologyNoticeOnceAndNamed(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workDir, registry := newRunTestWorkspace(t)
	writeWorkspaceTopology(t, workDir, "noverify",
		schemas.PipelineNode{Name: "code_writer", Type: "code_writer"},
	)
	var notices []string
	agentResult, err := Run(context.Background(), "add a Hello function", runFakeProvider{}, agent.Options{
		Cwd:              workDir,
		Registry:         registry,
		PermissionMode:   agent.PermissionModeAuto,
		FileTracker:      tools.NewFileTracker(),
		TrustedWorkspace: true,
		MaxTurns:         1,
		OnNotice:         func(text string) { notices = append(notices, text) },
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	count := 0
	for _, text := range notices {
		if strings.Contains(text, "no verification node") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("no-verification notices = %d (%q), want exactly 1", count, notices)
	}
	var result schemas.PipelineResult
	if err := json.Unmarshal([]byte(agentResult.FinalAnswer), &result); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if result.Verified {
		t.Fatal("verified = true on a topology with no verification node")
	}
	if want := "topology noverify has no verification node"; result.UnverifiedReason != want {
		t.Fatalf("unverified reason = %q, want %q", result.UnverifiedReason, want)
	}
}
