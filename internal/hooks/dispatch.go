package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/secrets"
)

// defaultHookTimeout bounds a single hook command so a hung or slow hook cannot
// stall the agent indefinitely.
const defaultHookTimeout = 30 * time.Second

// DispatchInput describes one lifecycle point at which hooks may run.
type DispatchInput struct {
	Event      Event
	ToolName   string // for beforeTool/afterTool matching
	ToolCallID string
	// Payload is serialized to JSON and written to each hook command's stdin so a
	// hook can inspect the tool call, its result, or session context.
	Payload any
	// Sandbox is the caller's agent sandbox engine. Plugin hooks run under it
	// outside a pipeline run. Nil (or an engine that does not enforce) makes
	// a plugin hook refuse to run unless the user opted the plugin into host
	// execution.
	Sandbox *sandbox.Engine
}

// DispatchOutcome reports what happened for one Dispatch call.
type DispatchOutcome struct {
	Ran       int    // hooks that executed
	Blocked   bool   // a blocking-event hook exited non-splice, vetoing the action
	BlockedBy string // ID of the hook that blocked (empty unless Blocked)
	Reason    string // the blocking hook's stderr/stdout, for surfacing to the model
	// Messages collects the output (stdout, else stderr) of each hook that
	// produced any, in run order. afterTool validators use this to feed results
	// (e.g. a formatter diff or vet warning) back to the model on the tool result.
	Messages []string
}

type commandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error // set when the command could not be executed (not a non-splice exit)
	TimedOut bool  // the hook started but its deadline/cancellation fired before it returned
	// Refused is set when the hook had to run sandboxed and no enforcing
	// sandbox was available. The command never started.
	Refused bool
}

// commandRunner executes one hook command. It is injectable so the dispatch
// logic can be tested without spawning processes.
type commandRunner func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult

// DispatcherOptions configures a Dispatcher.
type DispatcherOptions struct {
	Config  Config
	Audit   *AuditStore   // optional; when set, every run is recorded
	Cwd     string        // working directory for hook commands
	Env     []string      // extra environment entries appended to the process env
	Timeout time.Duration // per-command timeout (defaults to defaultHookTimeout)
	now     func() time.Time
	run     commandRunner
}

// Dispatcher selects and runs the hooks configured for a lifecycle event,
// recording each run to the audit store. A beforeTool hook that exits non-splice
// blocks the tool; hooks for other events are advisory (failures are recorded
// but do not interrupt the run).
type Dispatcher struct {
	config  Config
	audit   *AuditStore
	cwd     string
	env     []string
	timeout time.Duration
	now     func() time.Time
	run     commandRunner
	// pipelineEngine is the stage engine of a pipeline run (ForPipeline).
	// When set, every hook without runOnHost runs under it.
	pipelineEngine *sandbox.Engine
}

// NewDispatcher builds a Dispatcher. A splice/empty config yields a dispatcher that
// runs nothing, so callers can always construct one unconditionally.
func NewDispatcher(options DispatcherOptions) *Dispatcher {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	now := options.now
	if now == nil {
		now = time.Now
	}
	return &Dispatcher{
		config:  options.Config,
		audit:   options.Audit,
		cwd:     options.Cwd,
		env:     options.Env,
		timeout: timeout,
		now:     now,
		run:     options.run,
	}
}

// ForPipeline returns a copy of the dispatcher for one pipeline run. The copy
// runs every hook under engine (the run's stage engine) with cwd as the
// working directory, except a hook marked runOnHost. A nil dispatcher stays
// nil. The copy shares the config and audit store.
func (dispatcher *Dispatcher) ForPipeline(engine *sandbox.Engine, cwd string) *Dispatcher {
	if dispatcher == nil {
		return nil
	}
	bound := *dispatcher
	bound.pipelineEngine = engine
	if strings.TrimSpace(cwd) != "" {
		bound.cwd = cwd
	}
	return &bound
}

// Execution values recorded in the hook audit log.
const (
	ExecutionSandboxed = "sandboxed"
	ExecutionHost      = "host"
	ExecutionRefused   = "refused"
)

// hookPlacement is where one hook runs: under engine (sandboxed) or on the
// host. A sandboxed placement with a non-empty gap cannot run.
type hookPlacement struct {
	sandboxed bool
	engine    *sandbox.Engine
	gap       string
}

func (placement hookPlacement) execution() string {
	switch {
	case !placement.sandboxed:
		return ExecutionHost
	case placement.gap != "":
		return ExecutionRefused
	default:
		return ExecutionSandboxed
	}
}

// placeHook decides where a hook runs. A runOnHost hook runs on the host. In
// a pipeline run every other hook runs under the stage engine. Outside a
// pipeline run a plugin hook (third-party code) runs under the caller's agent
// engine and a user hook runs on the host.
func (dispatcher *Dispatcher) placeHook(hook Definition, input DispatchInput) hookPlacement {
	var engine *sandbox.Engine
	switch {
	case hook.RunOnHost:
		return hookPlacement{}
	case dispatcher.pipelineEngine != nil:
		engine = dispatcher.pipelineEngine
	case hook.PluginID != "":
		engine = input.Sandbox
	default:
		return hookPlacement{}
	}
	gap := engine.EnforcementGap()
	if hook.PluginDir != "" {
		// A user plugin lives under the Splice config directory, which the
		// default policy denies. The hook may read its own plugin directory
		// only. A user hook gets no such carve-out.
		engine = engine.WithReadableDir(hook.PluginDir)
	}
	return hookPlacement{sandboxed: true, engine: engine, gap: gap}
}

// hostOptInHint names the config key that lets this hook run on the host.
func hostOptInHint(hook Definition) string {
	if hook.PluginID != "" {
		return fmt.Sprintf("hook %q of plugin %q runs only inside the sandbox. To run it on the host, set plugins.%s.hostExecution to true in the user config.json.", hook.ID, hook.PluginID, hook.PluginID)
	}
	return fmt.Sprintf("hook %q runs inside the sandbox in a pipeline run. To run it on the host, set \"runOnHost\": true on it in the user hooks.json.", hook.ID)
}

// blocksOn reports whether a non-splice exit for this event should veto the action.
// Only beforeTool gates the tool; other events are observational.
func blocksOn(event Event) bool {
	return event == EventBeforeTool
}

// Config returns the dispatcher's hook configuration. The pipeline preflight
// uses it to statically warn about hooks that intercept splice.* stage tools
// before a run starts. A nil dispatcher returns an empty config.
func (dispatcher *Dispatcher) Config() Config {
	if dispatcher == nil {
		return Config{}
	}
	return dispatcher.config
}

// Dispatch runs every enabled hook configured for the input event (and matcher,
// for tool events). It returns once all hooks have run, or early if a blocking
// hook vetoes the action.
func (dispatcher *Dispatcher) Dispatch(ctx context.Context, input DispatchInput) DispatchOutcome {
	outcome := DispatchOutcome{}
	if dispatcher == nil {
		return outcome
	}
	selected := Select(dispatcher.config, SelectInput{Event: input.Event, ToolName: input.ToolName})
	if len(selected) == 0 {
		return outcome
	}

	payload, err := json.Marshal(input.Payload)
	if err != nil {
		payload = nil
	}

	for _, hook := range selected {
		command := Command{Command: hook.Command, Args: hook.Args}
		placement := dispatcher.placeHook(hook, input)
		dispatcher.recordStarted(hook, input, command, placement.execution())

		started := dispatcher.now()
		result := dispatcher.runPlaced(ctx, hook, placement, payload)
		durationMs := int(dispatcher.now().Sub(started) / time.Millisecond)
		outcome.Ran++

		status, blocked := classifyResult(input.Event, result)
		if placement.sandboxed && (status != AuditCompleted || result.Refused) {
			// Fail loudly: a sandboxed hook that failed may have hit the
			// sandbox, so the record and the notice name the hook and the
			// key that allows host execution.
			result.Stderr = strings.TrimSpace(strings.TrimSpace(result.Stderr) + "\n[splice] " + hostOptInHint(hook))
		}
		execution := placement.execution()
		if result.Refused {
			execution = ExecutionRefused
		}
		dispatcher.recordCompleted(hook, input, status, result, durationMs, execution)

		if message := hookMessage(result); message != "" {
			if placement.sandboxed && status != AuditCompleted && strings.TrimSpace(result.Stdout) != "" {
				message += "\n" + strings.TrimSpace(result.Stderr)
			}
			outcome.Messages = append(outcome.Messages, message)
		}

		if blocked {
			outcome.Blocked = true
			outcome.BlockedBy = hook.ID
			outcome.Reason = blockReason(result)
			// Stop on the first veto: the action is already denied.
			return outcome
		}
	}
	return outcome
}

func (dispatcher *Dispatcher) runPlaced(ctx context.Context, hook Definition, placement hookPlacement, stdin []byte) commandResult {
	if placement.sandboxed && placement.gap != "" {
		// Never fall back to the host: the hook does not start.
		procrun.ReportRefused(ctx, procrun.ProfileHooksDispatch, hook.Command, placement.engine, procrun.RefusalNoNativeSandbox, placement.gap)
		return commandResult{
			ExitCode: -1,
			Refused:  true,
			Err:      fmt.Errorf("hook %q must run sandboxed, but %s", hook.ID, placement.gap),
			Stderr:   fmt.Sprintf("hook %q was not run: it must run inside the sandbox, but %s.", hook.ID, placement.gap),
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, dispatcher.timeout)
	defer cancel()
	env := secrets.ScrubChildEnv(os.Environ())
	if len(dispatcher.env) > 0 {
		env = append(env, dispatcher.env...)
	}
	if hook.PluginDir != "" {
		// The plugin root contract: a plugin hook resolves its own files
		// through AGENT_PLUGIN_ROOT, like a plugin tool command.
		env = append(env, "AGENT_PLUGIN_ROOT="+hook.PluginDir)
	}
	run := dispatcher.run
	if run == nil {
		engine := placement.engine
		if !placement.sandboxed {
			engine = nil
		}
		run = func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
			return runHookCommand(ctx, engine, command, args, stdin, cwd, env)
		}
	}
	result := run(runCtx, hook.Command, hook.Args, stdin, dispatcher.cwd, env)
	// A deadline or cancellation that fired while the hook was running is distinct
	// from a launch failure: the hook DID start, we just never got its verdict.
	// classifyResult must fail CLOSED on this for a blocking event — a hung policy
	// hook cannot be allowed to wave the tool through.
	if runCtx.Err() != nil {
		result.TimedOut = true
	}
	return result
}

// classifyResult maps a command result to an audit status and whether it vetoes
// the action. A command that ran and exited non-splice blocks a beforeTool hook; a
// command that could not be executed at all is an error but never blocks (a
// missing hook binary must not wedge every tool call).
func classifyResult(event Event, result commandResult) (AuditStatus, bool) {
	// A hook that had to run sandboxed but could not never gave a verdict.
	// Like a timeout, a blocking event fails CLOSED.
	if result.Refused {
		if blocksOn(event) {
			return AuditBlocked, true
		}
		return AuditError, false
	}
	// A hook that started but was killed by its deadline (or a cancellation) never
	// returned a verdict. For a blocking event we must fail CLOSED and veto: a
	// hung beforeTool policy hook cannot be treated as approval. (A launch failure
	// below still fails OPEN, so a misconfigured hook doesn't wedge every tool.)
	if result.TimedOut {
		if blocksOn(event) {
			return AuditBlocked, true
		}
		return AuditError, false
	}
	if result.Err != nil {
		return AuditError, false
	}
	if result.ExitCode != 0 {
		if blocksOn(event) {
			return AuditBlocked, true
		}
		return AuditError, false
	}
	return AuditCompleted, false
}

// hookMessage returns the output worth surfacing from a hook run: stdout when
// present, else stderr. Empty when the hook produced no output.
func hookMessage(result commandResult) string {
	if trimmed := strings.TrimSpace(result.Stdout); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(result.Stderr)
}

func blockReason(result commandResult) string {
	if result.TimedOut {
		if trimmed := strings.TrimSpace(result.Stderr); trimmed != "" {
			return "hook timed out: " + trimmed
		}
		return "hook timed out before returning a verdict"
	}
	for _, candidate := range []string{result.Stderr, result.Stdout} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return "hook exited non-splice"
}

func (dispatcher *Dispatcher) recordStarted(hook Definition, input DispatchInput, command Command, execution string) {
	if dispatcher.audit == nil {
		return
	}
	_, _ = dispatcher.audit.AppendStarted(AppendStartedInput{
		HookID:     hook.ID,
		Event:      input.Event,
		Matcher:    hook.Matcher,
		ToolCallID: input.ToolCallID,
		Commands:   []AuditCommand{command},
		Execution:  execution,
		PluginID:   hook.PluginID,
	})
}

func (dispatcher *Dispatcher) recordCompleted(hook Definition, input DispatchInput, status AuditStatus, result commandResult, durationMs int, execution string) {
	if dispatcher.audit == nil {
		return
	}
	_, _ = dispatcher.audit.AppendCompleted(AppendCompletedInput{
		HookID:     hook.ID,
		Event:      input.Event,
		Matcher:    hook.Matcher,
		ToolCallID: input.ToolCallID,
		Status:     status,
		Results:    []AuditResult{{ExitCode: result.ExitCode, Stdout: result.Stdout, Stderr: result.Stderr}},
		DurationMs: durationMs,
		Execution:  execution,
		PluginID:   hook.PluginID,
	})
}

// execCommandRunner runs a hook command on the host. See runHookCommand.
func execCommandRunner(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
	return runHookCommand(ctx, nil, command, args, stdin, cwd, env)
}

// runHookCommand runs a hook command directly (no shell), feeding the JSON
// payload on stdin and capturing stdout/stderr. A non-splice exit is reported via
// ExitCode (not Err); Err is reserved for commands that could not be launched.
// The command goes through the procrun chokepoint so every hook spawn emits an
// audit record. A nil engine runs the command on the host; a non-nil engine
// must wrap it, or the hook is refused. The environment is the scrubbed
// process env plus the dispatcher extras. The process group is killed when
// the hook times out, like the bash tool.
func runHookCommand(ctx context.Context, engine *sandbox.Engine, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
	prepared, err := procrun.Prepare(ctx, procrun.Request{
		ProfileID: procrun.ProfileHooksDispatch,
		Engine:    engine,
		Spec: sandbox.CommandSpec{
			Name: command,
			Args: args,
			Dir:  cwd,
			Env:  env,
		},
	})
	if err != nil {
		// A refusal must not look like a clean non-splice exit.
		if engine != nil {
			return commandResult{ExitCode: -1, Refused: true, Err: err, Stderr: "hook was not run: " + err.Error()}
		}
		return commandResult{ExitCode: -1, Err: err}
	}
	if engine != nil && !prepared.Plan.Wrapped {
		// An inherited sandbox marker can leave the plan unwrapped. That is
		// not confinement, so the hook does not start.
		procrun.ReportExit(ctx, prepared.Cmd, procrun.SpawnExit{ExitCode: -1, Refused: procrun.RefusalNoNativeSandbox})
		return commandResult{ExitCode: -1, Refused: true, Err: errors.New("the sandbox did not wrap the hook command"), Stderr: "hook was not run: the sandbox did not wrap the hook command."}
	}
	cmd := prepared.Cmd
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	procrun.ReportRun(ctx, cmd, err)
	result := commandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result
	}
	// Could not launch (binary missing, timeout, etc.).
	result.ExitCode = -1
	result.Err = err
	return result
}
