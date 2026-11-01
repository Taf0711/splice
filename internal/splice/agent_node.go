package splice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/tools"
)

// agentNodeFraming opens the user message of every agent node. It names the
// node's role and the output contract. The task text follows it.
const agentNodeFraming = "You are the agent node %q in a deterministic coding pipeline. " +
	"Use only the tools you are given. Finish with a concise hand-off summary of what you determined or changed."

// agentNodeSystemAddendum is appended to the agent's system prompt. It carries
// the same data-block rule as a prompt node, with the same sentence.
const agentNodeSystemAddendum = stages.DataBlockRule

// agentNodePermissionHandler limits a node's approval prompts to allow and
// deny. The request offers only those two decisions. A session, prefix, or
// always grant from the handler is refused, so a node cannot widen its own
// approvals beyond the one call it asked about. A cancel passes through, so
// the user's cancel stops the run.
func agentNodePermissionHandler(handler func(context.Context, agent.PermissionRequest) (agent.PermissionDecision, error)) func(context.Context, agent.PermissionRequest) (agent.PermissionDecision, error) {
	if handler == nil {
		return nil
	}
	return func(ctx context.Context, request agent.PermissionRequest) (agent.PermissionDecision, error) {
		request.AvailableDecisions = []agent.PermissionDecisionAction{agent.PermissionDecisionAllow, agent.PermissionDecisionDeny}
		decision, err := handler(ctx, request)
		if err != nil {
			return decision, err
		}
		switch decision.Action {
		case agent.PermissionDecisionAllow, agent.PermissionDecisionDeny, agent.PermissionDecisionCancel:
			return decision, nil
		}
		return agent.PermissionDecision{Action: agent.PermissionDecisionDeny, Reason: "agent nodes accept only allow or deny for this request"}, nil
	}
}

// agentNodeRunLine describes one agent node as it will run under a run mode:
// the effective permission after the floor and the unsafe cap, the sandbox
// statement, and the note that unsafe is capped. The run-start notice uses it,
// so the notice names the mode the node really gets.
func agentNodeRunLine(node schemas.PipelineNode, runMode agent.PermissionMode) string {
	effective := effectiveAgentPermission(runMode, node.Permission)
	turns := node.MaxTurns
	if turns == 0 {
		turns = schemas.AgentDefaultMaxTurns
	}
	line := fmt.Sprintf("%s: tools %s; effective permission %s; sandboxed; up to %d turns; %s", node.Name, strings.Join(node.Tools, ", "), agentPermissionText(string(effective)), turns, agentNodeBudgetText(node))
	if runMode == agent.PermissionModeUnsafe {
		line += "; unsafe mode is capped at auto"
	}
	return sanitizeNoticeLine(line)
}

// agentNodeRun is one agent node invocation. It captures the run's policy at
// the stage boundary, so the loop can use only what the run already allows.
type agentNodeRun struct {
	stage     string
	iteration int
	selection agent.ModelSelection
	options   PipelineRunConfig
	workDir   string
}

// agentNodeRunner returns the RunAgent seam for one stage invocation. The
// stage package calls it and never imports the agent loop.
func agentNodeRunner(stage string, iteration int, selection agent.ModelSelection, options PipelineRunConfig, workDir string) func(context.Context, stages.AgentNodeRequest) (stages.AgentNodeResult, error) {
	run := agentNodeRun{stage: stage, iteration: iteration, selection: selection, options: options, workDir: workDir}
	return run.run
}

// effectiveAgentPermission returns the permission mode an agent node runs
// under. A node can only tighten the run. "ask" wins over every run mode. A
// run in "ask" stays "ask". An "unsafe" run is capped at "auto", because an
// agent node never runs unsandboxed. "auto" tightens nothing further. Any
// other run mode, such as "spec-draft" or "member-auto", passes through unless
// the node says "ask". An empty run mode is "auto", as the loop defaults it.
func effectiveAgentPermission(runMode agent.PermissionMode, nodePermission string) agent.PermissionMode {
	if runMode == "" || runMode == agent.PermissionModeUnsafe {
		runMode = agent.PermissionModeAuto
	}
	if nodePermission == schemas.AgentPermissionAsk || runMode == agent.PermissionModeAsk {
		return agent.PermissionModeAsk
	}
	return runMode
}

func hasAgentToolName(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// agentNodeRefusal reports whether a tool result is a refusal, and why. A
// refusal is any call the loop or the registry blocked before it ran. The
// category is typed, so the check does not depend on message wording.
func agentNodeRefusal(result agent.ToolResult) (string, bool) {
	if result.DenialReason != agent.DenialNone {
		return string(result.DenialReason), true
	}
	return "", false
}

// run executes one agent node's loop. It enforces the allowlist and the
// permission floor through the loop's own filters, caps the output budget, and
// fails on the first refused tool call. Every failure names the node through
// the stage wrapper.
func (r agentNodeRun) run(ctx context.Context, req stages.AgentNodeRequest) (stages.AgentNodeResult, error) {
	opts := r.options
	if opts.Registry == nil {
		return stages.AgentNodeResult{}, fmt.Errorf("no tool registry is available for tools %s", strings.Join(req.Tools, ", "))
	}
	if r.selection.Provider == nil {
		return stages.AgentNodeResult{}, fmt.Errorf("no provider is available for the model")
	}
	if err := agentNodeSandboxError(opts.Sandbox); err != nil {
		return stages.AgentNodeResult{}, err
	}
	// A declared tool the run disables fails the node at start. The node never
	// runs without a tool it declared, and no tool is dropped silently.
	for _, name := range req.Tools {
		if hasAgentToolName(opts.DisabledTools, name) || (len(opts.EnabledTools) > 0 && !hasAgentToolName(opts.EnabledTools, name)) {
			return stages.AgentNodeResult{}, fmt.Errorf("tool %q is not enabled for this run; the node cannot run without it", name)
		}
	}
	allowed := req.Tools
	// The loop exempts tool_search from its allowlist so the model can always
	// discover tools. An agent node must not reach past its allowlist, so the
	// node disables it explicitly.
	disabled := append(append([]string(nil), opts.DisabledTools...), tools.ToolSearchToolName)
	maxTurns := req.MaxTurns
	if maxTurns <= 0 {
		maxTurns = schemas.AgentDefaultMaxTurns
	}
	sandbox := opts.Sandbox
	if sandbox != nil {
		sandbox = sandbox.ReRooted(r.workDir)
	}

	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The loop may run tool calls in parallel, so the callbacks share state
	// under one lock.
	var (
		mu           sync.Mutex
		usage        schemas.StageUsage
		usageSeen    bool
		inputTokens  int
		outputTokens int
		budgetHit    string
		usageMissing bool
		refusal      string
		canceled     string
		changed      []string
	)
	agentOpts := agent.Options{
		MaxTurns:            maxTurns,
		Registry:            opts.Registry,
		PermissionMode:      effectiveAgentPermission(opts.PermissionMode, req.Permission),
		Autonomy:            opts.Autonomy,
		TrustedWorkspace:    opts.TrustedWorkspace,
		Sandbox:             sandbox,
		FileTracker:         opts.FileTracker,
		Hooks:               opts.Hooks,
		EnabledTools:        allowed,
		DisabledTools:       disabled,
		Cwd:                 r.workDir,
		ProjectRoot:         opts.ProjectRoot,
		SessionID:           opts.SessionID,
		ProviderName:        r.selection.ProviderName,
		Model:               r.selection.Model,
		ReasoningEffort:     r.selection.ReasoningEffort,
		OnToolCall:          func(call agent.ToolCall) { emitToolCall(opts, call) },
		OnToolCallStart:     opts.OnToolCallStart,
		OnToolCallDelta:     opts.OnToolCallDelta,
		OnPermissionRequest: agentNodePermissionHandler(opts.OnPermissionRequest),
		OnPermission:        opts.OnPermission,
		OnToolProgress:      opts.OnToolProgress,
		OnToolOutput:        opts.OnToolOutput,
		FileDiagnostics:     opts.FileDiagnostics,
		// The stage guard reaches agent nodes too, so a blind overwrite is refused.
		RequireReadBeforeWrite:  opts.StageRequireReadBeforeWrite,
		EstimateUsageCost:       opts.EstimateUsageCost,
		EstimateServedUsageCost: opts.EstimateServedUsageCost,
		RequireCompletionSignal: true,
		SystemAddendum:          agentNodeSystemAddendum,
		NoSandboxEscape:         true,
	}
	agentOpts.OnAttributedUsage = func(attributed agent.AttributedUsage) {
		attributed.Stage = r.stage
		attributed.Iteration = r.iteration
		mu.Lock()
		defer mu.Unlock()
		if attributed.UsageError == "" {
			u := attributed.Usage
			usage.InputTokens += u.EffectiveInputTokens()
			usage.OutputTokens += u.EffectiveOutputTokens()
			usage.CachedInputTokens += u.CachedInputTokens
			usage.CacheWriteTokens += u.CacheWriteTokens
			usage.ReasoningTokens += u.ReasoningTokens
			usage.WebSearchRequests += u.WebSearchRequests
			usageSeen = true
			inputTokens += u.EffectiveInputTokens()
			outputTokens += u.EffectiveOutputTokens()
		}
		if opts.OnAttributedUsage != nil {
			opts.OnAttributedUsage(attributed)
		}
		// A request with no usage, or malformed usage, counts zero tokens, so
		// the budget cannot be enforced. The node fails closed. A request that
		// the node itself stopped is not counted, so the first stop keeps its
		// reason. A stream that failed or was canceled did not complete, so its
		// missing usage does not show a provider that never reports usage. The
		// loop can still retry that request (a stall retry or a reactive
		// compaction).
		if (attributed.UsageError != "" || (!attributed.UsageReported && !attributed.StreamFailed)) && (req.InputBudget > 0 || req.OutputBudget > 0) &&
			budgetHit == "" && !usageMissing && loopCtx.Err() == nil {
			usageMissing = true
			cancel()
		}
		// The loop stops at the first budget it exceeds. Input is summed over
		// every request, because each request sends the whole context again.
		if budgetHit == "" {
			switch {
			case req.InputBudget > 0 && inputTokens > req.InputBudget:
				budgetHit = "input"
				cancel()
			case req.OutputBudget > 0 && outputTokens > req.OutputBudget:
				budgetHit = "output"
				cancel()
			}
		}
	}
	agentOpts.OnToolResult = func(result agent.ToolResult) {
		mu.Lock()
		defer mu.Unlock()
		for _, file := range result.ChangedFiles {
			if !hasAgentToolName(changed, file) {
				changed = append(changed, file)
			}
		}
		if result.DenialReason == agent.DenialApprovalCanceled {
			// The user canceled an approval prompt. That cancels the run, not
			// only the node.
			if canceled == "" {
				canceled = result.Name
			}
			cancel()
		} else if reason, refused := agentNodeRefusal(result); refused && refusal == "" {
			refusal = fmt.Sprintf("tool %q refused (%s): %s", result.Name, reason, strings.TrimSpace(result.Output))
			cancel()
		}
		if opts.OnToolResult != nil {
			opts.OnToolResult(result)
		}
	}

	prompt := fmt.Sprintf(agentNodeFraming, r.stage) + "\n\n" + req.Prompt
	loopResult, err := agent.Run(loopCtx, prompt, r.selection.Provider, agentOpts)
	out := stages.AgentNodeResult{ChangedFiles: changed}
	if usageSeen {
		out.Usage = &usage
	}
	switch {
	case canceled != "":
		return out, fmt.Errorf("approval for tool %q was canceled: %w", canceled, context.Canceled)
	case budgetHit == "input":
		return out, fmt.Errorf("input budget of %d tokens exhausted after %d tokens", req.InputBudget, inputTokens)
	case budgetHit == "output":
		return out, fmt.Errorf("output budget of %d tokens exhausted after %d tokens", req.OutputBudget, outputTokens)
	case refusal != "":
		return out, errors.New(refusal)
	case usageMissing:
		return out, errors.New("usage not reported; the budget cannot be enforced")
	case err != nil:
		return out, err
	case loopResult.Incomplete && loopResult.Turns >= maxTurns:
		return out, fmt.Errorf("turn cap of %d reached before the task completed", maxTurns)
	case loopResult.StopReason != "":
		return out, fmt.Errorf("loop stopped (%s) before the task completed: %s", loopResult.StopReason, loopResult.IncompleteReason)
	case loopResult.Incomplete:
		return out, fmt.Errorf("stopped before the task completed: %s", loopResult.IncompleteReason)
	case loopResult.Truncated():
		return out, fmt.Errorf("final answer was cut off (%s)", loopResult.FinishReason)
	}
	out.Answer = loopResult.FinalAnswer
	return out, nil
}

// agentNodeSandboxError returns why an agent node cannot run under engine, or
// nil when engine enforces its policy with the native sandbox. The run start
// and each node use it, so both refuse with the same error.
func agentNodeSandboxError(engine *sandbox.Engine) error {
	if engine == nil {
		return errors.New("no sandbox engine is available; an agent node never runs unsandboxed")
	}
	if gap := engine.EnforcementGap(); gap != "" {
		return fmt.Errorf("agent nodes need an enforcing sandbox; %s", gap)
	}
	return nil
}
