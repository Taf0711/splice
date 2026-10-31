package stages

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestAgentStageRefusesWithoutLoopRunner(t *testing.T) {
	stage := AgentStage{Name: "investigate", Template: "find {{intent}}", Tools: []string{"grep"}}
	_, err := stage.Run(context.Background(), schemas.HarnessStageInput{}, nil, StageOptions{})
	if err == nil || !strings.Contains(err.Error(), "agent node investigate: no agent loop is available") {
		t.Fatalf("Run() error = %v, want a named refusal when no loop runner is wired", err)
	}
}

func TestAgentStageFailsLoudOnBadInput(t *testing.T) {
	run := func(stage AgentStage) error {
		opts := StageOptions{RunAgent: func(context.Context, AgentNodeRequest) (AgentNodeResult, error) {
			return AgentNodeResult{Answer: "ok"}, nil
		}}
		_, err := stage.Run(context.Background(), schemas.HarnessStageInput{}, nil, opts)
		return err
	}
	if err := run(AgentStage{Name: "a", Template: " ", Tools: []string{"grep"}}); err == nil || !strings.Contains(err.Error(), "agent node a: template is empty") {
		t.Fatalf("empty template error = %v", err)
	}
	if err := run(AgentStage{Name: "a", Template: "x"}); err == nil || !strings.Contains(err.Error(), "agent node a: tools allowlist is empty") {
		t.Fatalf("empty allowlist error = %v", err)
	}
}

func TestAgentStageNamesLoopErrorAndKeepsUsage(t *testing.T) {
	usage := &schemas.StageUsage{OutputTokens: 77}
	stage := AgentStage{Name: "investigate", Template: "x", Tools: []string{"grep"}}
	opts := StageOptions{RunAgent: func(context.Context, AgentNodeRequest) (AgentNodeResult, error) {
		return AgentNodeResult{Usage: usage}, errors.New("turn cap of 4 reached before the task completed")
	}}
	_, err := stage.Run(context.Background(), schemas.HarnessStageInput{}, nil, opts)
	if err == nil || !strings.Contains(err.Error(), "agent node investigate: turn cap of 4 reached") {
		t.Fatalf("Run() error = %v, want the node named", err)
	}
	metered, ok := err.(interface{ StageUsage() *schemas.StageUsage })
	if !ok || metered.StageUsage() == nil || metered.StageUsage().OutputTokens != 77 {
		t.Fatalf("failed node lost its usage: %v", err)
	}
}

func TestAgentStageMapsAnswerAndChangedFiles(t *testing.T) {
	var got AgentNodeRequest
	stage := AgentStage{Name: "investigate", Template: "find {{intent}}", Tools: []string{"grep", "read_file"}, MaxTurns: 3, Permission: "ask", InputMax: 2000, OutputMax: 500}
	opts := StageOptions{
		// The node's explicit budget wins over the fitted per-request cap.
		MaxOutputTokens: 9999,
		RunAgent: func(_ context.Context, request AgentNodeRequest) (AgentNodeResult, error) {
			got = request
			return AgentNodeResult{Answer: "  The parser is in parse.go.\nIt is small.  ", ChangedFiles: []string{"parse.go"}, Usage: &schemas.StageUsage{OutputTokens: 9}}, nil
		},
	}
	input := schemas.HarnessStageInput{RequestIntent: "parsing"}
	out, err := stage.Run(context.Background(), input, nil, opts)
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if got.Prompt != "find parsing" || got.MaxTurns != 3 || got.Permission != "ask" || got.InputBudget != 2000 || got.OutputBudget != 500 {
		t.Fatalf("request = %+v, want the rendered prompt, turns, permission, and the node budget", got)
	}
	if strings.Join(got.Tools, ",") != "grep,read_file" {
		t.Fatalf("tools = %v", got.Tools)
	}
	if out.Summary != "The parser is in parse.go." {
		t.Fatalf("summary = %q, want the first line", out.Summary)
	}
	if out.Data["agent_output"] != "The parser is in parse.go.\nIt is small." {
		t.Fatalf("agent_output = %q", out.Data["agent_output"])
	}
	if len(out.ChangedFiles) != 1 || out.ChangedFiles[0] != "parse.go" {
		t.Fatalf("changed files = %v", out.ChangedFiles)
	}
	if out.Usage == nil || out.Usage.OutputTokens != 9 {
		t.Fatalf("usage = %+v", out.Usage)
	}
}

func TestAgentStageEmptyAnswerFails(t *testing.T) {
	stage := AgentStage{Name: "investigate", Template: "x", Tools: []string{"grep"}}
	opts := StageOptions{RunAgent: func(context.Context, AgentNodeRequest) (AgentNodeResult, error) {
		return AgentNodeResult{Answer: "   ", ChangedFiles: []string{"parse.go"}}, nil
	}}
	_, err := stage.Run(context.Background(), schemas.HarnessStageInput{}, nil, opts)
	if err == nil || !strings.Contains(err.Error(), "agent node investigate: loop returned no output") {
		t.Fatalf("Run() error = %v, want the empty answer named", err)
	}
	// The node's edits still reach the pass evidence when the answer is empty.
	changed, ok := err.(interface{ StageChangedFiles() []string })
	if !ok || strings.Join(changed.StageChangedFiles(), ",") != "parse.go" {
		t.Fatalf("empty-answer failure lost the changed files: %v", err)
	}
}
