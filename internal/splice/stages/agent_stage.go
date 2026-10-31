package stages

import (
	"context"
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// AgentNodeRequest is the input of one agent node run. It carries only what
// the node declares. The orchestrator adds the run's policy, route, and
// sandbox when it runs the loop.
type AgentNodeRequest struct {
	// Name is the node name. It labels errors and budget messages.
	Name string
	// Prompt is the rendered node template, the task text for the loop.
	Prompt string
	// Tools is the node's tool allowlist, exactly as declared.
	Tools []string
	// MaxTurns is the declared turn cap. Zero means the schema default.
	MaxTurns int
	// Permission is the node's declared permission, "ask", "auto", or empty.
	Permission string
	// InputBudget caps the total input tokens of the loop, summed over every
	// request. Zero means no cap.
	InputBudget int
	// OutputBudget caps the total output tokens of the loop. Zero means no cap.
	OutputBudget int
}

// AgentNodeResult is what one agent node run returns. Usage and ChangedFiles
// are set even when the run returns an error, so the spend and the file
// evidence of a failed node still count.
type AgentNodeResult struct {
	// Answer is the loop's final assistant text.
	Answer string
	// ChangedFiles lists the paths the node's tool results changed.
	ChangedFiles []string
	// Usage is the loop's total usage, nil when the provider reported none.
	Usage *schemas.StageUsage
}

// maxAgentOutputChars bounds the final text of an agent node. The text is
// untrusted model output, so the bound is the same as a prompt node's.
const maxAgentOutputChars = maxPromptOutputChars

// AgentStage runs one topology agent node: a bounded, tool-using model loop
// whose tools are an explicit allowlist. The loop itself is provided through
// StageOptions.RunAgent. This stage only renders the template and maps the
// result to a stage output.
type AgentStage struct {
	// Name is the node name; it labels errors and activity.
	Name string
	// Template is the node's task template, rendered like a prompt node's.
	Template string
	// Tools is the node's tool allowlist.
	Tools []string
	// MaxTurns is the node's turn cap. Zero means the schema default.
	MaxTurns int
	// Permission is the node's declared permission. Empty means the run's mode.
	Permission string
	// InputMax and OutputMax are the node's explicit budget, from budget.input_max
	// and budget.output_max. The loop stops when either is exceeded.
	InputMax  int
	OutputMax int
}

var _ Stage = AgentStage{}

// Capabilities reports an agent node as model-backed with no context or
// memory pull by default.
func (s AgentStage) Capabilities() Capabilities {
	return Capabilities{ModelFree: false, PullContext: false, ConsumesMemory: false, Description: "running agent " + s.Name}
}

// Run renders the template and runs the node's loop. It fails loud on an empty
// template, a missing loop runner, an error from the loop, or an empty answer.
// Every error names the node.
func (s AgentStage) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options StageOptions) (schemas.HarnessStageOutput, error) {
	if strings.TrimSpace(s.Template) == "" {
		return schemas.HarnessStageOutput{}, fmt.Errorf("agent node %s: template is empty", s.Name)
	}
	if len(s.Tools) == 0 {
		return schemas.HarnessStageOutput{}, fmt.Errorf("agent node %s: tools allowlist is empty", s.Name)
	}
	if options.RunAgent == nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("agent node %s: no agent loop is available; refusing to run outside the registry path", s.Name)
	}
	options.report("running agent " + s.Name)
	result, err := options.RunAgent(ctx, AgentNodeRequest{
		Name:         s.Name,
		Prompt:       renderPromptTemplate(s.Template, input),
		Tools:        append([]string(nil), s.Tools...),
		MaxTurns:     s.MaxTurns,
		Permission:   s.Permission,
		InputBudget:  s.InputMax,
		OutputBudget: s.OutputMax,
	})
	if err != nil {
		return schemas.HarnessStageOutput{}, meteredStageError{err: fmt.Errorf("agent node %s: %w", s.Name, err), usage: result.Usage, changed: result.ChangedFiles}
	}
	answer := strings.TrimSpace(result.Answer)
	if answer == "" {
		return schemas.HarnessStageOutput{}, meteredStageError{err: fmt.Errorf("agent node %s: loop returned no output", s.Name), usage: result.Usage, changed: result.ChangedFiles}
	}
	output := truncateRunes(answer, maxAgentOutputChars)
	return schemas.HarnessStageOutput{
		Summary:      promptFirstLine(output),
		Detail:       output,
		Confidence:   0.5,
		ChangedFiles: append([]string(nil), result.ChangedFiles...),
		Data: map[string]any{
			"agent_output": output,
		},
		Usage: result.Usage,
	}, nil
}
