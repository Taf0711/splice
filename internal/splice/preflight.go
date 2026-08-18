package splice

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/hooks"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// IssueSeverityWarn is the only severity preflight emits: preflight diagnoses,
// never blocks. User machinery (permission mode, hooks) stays authoritative.
const IssueSeverityWarn = "warn"

// Issue is one preflight finding. Severity is always "warn".
type Issue struct {
	Severity string
	Stage    string
	Message  string
}

// stageSpliceTools maps a stage name to the splice.* stage tools it invokes.
// These are the pipeline's own shell/test identities: static_analyzer and
// security_auditor run shell commands (splice.shell), test_runner runs the
// test command (splice.test). A stage absent here needs no splice tool.
//
// Pairing coverage: TestStageSpliceToolsKeysAreRealStages pins that every key
// here is a real stage, so renames and deletions fail CI. Additions are NOT
// statically caught: a new stage that starts invoking splice.shell without
// being added to this map is silent. If that gap ever matters, declare tool
// requirements on stages.Capabilities and derive this map from them instead
// of maintaining it by hand.
var stageSpliceTools = map[string][]string{
	"static_analyzer":  {"splice.shell"},
	"security_auditor": {"splice.shell"},
	"test_runner":      {"splice.test"},
}

// Preflight diagnoses substrate interference before a run starts: user
// machinery (permission mode, hooks) and provider capability that would
// otherwise break the run mid-flight even though the error lands named. It is
// pure and advisory: callers emit each issue as a warning and continue.
func Preflight(plan schemas.ExecutionPlan, options PipelineRunConfig) []Issue {
	var issues []Issue
	issues = append(issues, preflightPermissionMode(plan, options)...)
	issues = append(issues, preflightHooks(plan, options)...)
	issues = append(issues, preflightProviderCapability(plan, options)...)
	return issues
}

// preflightPermissionMode warns per stage when the permission mode would
// prompt or deny a splice.* stage tool mid-run. The pipeline's auto,
// spec-draft, unsafe, and member-auto modes grant prompt-gated tools
// automatically; ask mode prompts (and denies when no approver is wired).
func preflightPermissionMode(plan schemas.ExecutionPlan, options PipelineRunConfig) []Issue {
	if options.PermissionMode != agent.PermissionModeAsk {
		return nil
	}
	var issues []Issue
	for _, stage := range plan.Stages {
		for _, tool := range stageSpliceTools[stage.Name] {
			if options.OnPermissionRequest == nil {
				issues = append(issues, Issue{
					Severity: IssueSeverityWarn,
					Stage:    stage.Name,
					Message:  fmt.Sprintf("%s will be denied", tool),
				})
				continue
			}
			issues = append(issues, Issue{
				Severity: IssueSeverityWarn,
				Stage:    stage.Name,
				Message:  fmt.Sprintf("%s may prompt mid-run", tool),
			})
		}
	}
	return issues
}

// preflightHooks warns about enabled beforeTool hooks whose matcher can
// intercept a splice.* stage tool. A hook with an empty matcher (or "*")
// matches every tool and is reported against each splice tool; a targeted
// matcher is reported only for the tool it matches.
func preflightHooks(plan schemas.ExecutionPlan, options PipelineRunConfig) []Issue {
	if options.Hooks == nil {
		return nil
	}
	config := options.Hooks.Config()
	if !config.Enabled {
		return nil
	}

	// Only warn for splice tools a stage in this plan actually uses.
	requiredSet := make(map[string]bool)
	for _, stage := range plan.Stages {
		for _, tool := range stageSpliceTools[stage.Name] {
			requiredSet[tool] = true
		}
	}
	if len(requiredSet) == 0 {
		return nil
	}
	required := make([]string, 0, len(requiredSet))
	for tool := range requiredSet {
		required = append(required, tool)
	}
	slices.Sort(required)

	var issues []Issue
	seen := make(map[string]bool)
	for _, hook := range config.Hooks {
		if !hook.Enabled || hook.Event != hooks.EventBeforeTool {
			continue
		}
		name := hook.Name
		if name == "" {
			name = hook.ID
		}
		for _, tool := range required {
			// Check this hook alone against the tool so a multi-hook config
			// does not misattribute one hook's matcher to the others.
			single := hooks.Config{Enabled: true, Hooks: []hooks.Definition{hook}}
			if len(hooks.Select(single, hooks.SelectInput{Event: hooks.EventBeforeTool, ToolName: tool})) == 0 {
				continue
			}
			key := name + "\x00" + tool
			if seen[key] {
				continue
			}
			seen[key] = true
			issues = append(issues, Issue{
				Severity: IssueSeverityWarn,
				Stage:    "",
				Message:  fmt.Sprintf("hook %q may intercept %s", name, tool),
			})
		}
	}
	return issues
}

// preflightProviderCapability warns when the active model's static catalog
// entry declares no tool-calling support. An empty model id, an unknown model,
// or an absent registry is "not statically knowable" and skipped silently;
// this never performs a live provider probe.
func preflightProviderCapability(plan schemas.ExecutionPlan, options PipelineRunConfig) []Issue {
	model := strings.TrimSpace(options.Model)
	if model == "" {
		return nil
	}
	// A zero Registry (entries nil) is the unset case: Get returns false and the
	// check is skipped silently, exactly like an unknown model. Never a live probe.
	entry, ok := options.ModelRegistry.Get(model)
	if !ok {
		return nil
	}
	if entry.Supports(modelregistry.ModelCapabilityToolCalling) {
		return nil
	}
	// Tool-calling is required by every LLM-backed stage; report once, named
	// for the first LLM stage in the plan (or without a stage when none runs).
	stage := ""
	for _, s := range plan.Stages {
		if !modelFreeStage(s.Name) {
			stage = s.Name
			break
		}
	}
	return []Issue{{
		Severity: IssueSeverityWarn,
		Stage:    stage,
		Message:  "model may not support tool calling",
	}}
}

// modelFreeStage reports whether a stage name is a deterministic (model-free)
// pipeline stage. Used to name the LLM stage a capability issue affects.
func modelFreeStage(name string) bool {
	switch name {
	case "static_analyzer", "security_auditor", "test_runner", "acceptance_verifier":
		return true
	default:
		return false
	}
}
