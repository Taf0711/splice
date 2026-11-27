package schemas

import (
	"errors"
	"fmt"
	"regexp"
)

// MaxSandboxEvidencePerStage bounds the sandbox evidence of one stage
// record. Spawns past the bound are counted in SandboxEvidenceDropped.
const MaxSandboxEvidencePerStage = 50

// SandboxSpawnSource names the surface that spawned a command.
type SandboxSpawnSource string

const (
	SandboxSourceBashTool        SandboxSpawnSource = "bash_tool"
	SandboxSourceExecCommandTool SandboxSpawnSource = "exec_command_tool"
	// SandboxSourceStageCommand is a deterministic stage spawn (the
	// splice.stage procrun profile).
	SandboxSourceStageCommand SandboxSpawnSource = "stage_command"
	// SandboxSourceScannerTool is a scanner tool spawn (the splice.dtools
	// procrun profile: bandit, gosec, SARIF scanners).
	SandboxSourceScannerTool SandboxSpawnSource = "scanner_tool"
	SandboxSourcePluginTool  SandboxSpawnSource = "plugin_tool"
	SandboxSourceHook        SandboxSpawnSource = "hook"
	SandboxSourceOther       SandboxSpawnSource = "other"
)

// SandboxPolicySource names which policy a spawn ran under.
type SandboxPolicySource string

const (
	// SandboxPolicyStage is the stage policy derived from the user policy.
	SandboxPolicyStage SandboxPolicySource = "stage"
	// SandboxPolicyUser is the interactive user policy.
	SandboxPolicyUser SandboxPolicySource = "user"
	// SandboxPolicyNone is a spawn with no sandbox engine (host execution).
	SandboxPolicyNone SandboxPolicySource = "none"
)

// SandboxOutcome is what happened to one spawn.
type SandboxOutcome string

const (
	// SandboxOutcomeRan: the command started.
	SandboxOutcomeRan SandboxOutcome = "ran"
	// SandboxOutcomeRefused: the command did not start. Refusal names why.
	SandboxOutcomeRefused SandboxOutcome = "refused"
	// SandboxOutcomeDenied: the command started and failed with the signs of
	// a sandbox denial.
	SandboxOutcomeDenied SandboxOutcome = "denied"
	// SandboxOutcomeTimedOut: the command started and its deadline killed it.
	SandboxOutcomeTimedOut SandboxOutcome = "timed_out"
)

// SandboxRefusal is the category of a refused spawn.
type SandboxRefusal string

const (
	SandboxRefusalNoEnforcingSandbox SandboxRefusal = "no_enforcing_sandbox"
	SandboxRefusalEscalation         SandboxRefusal = "escalation_refused"
	SandboxRefusalPermissionDenied   SandboxRefusal = "permission_denied"
	SandboxRefusalHookBlocked        SandboxRefusal = "hook_blocked"
	SandboxRefusalBinaryNotAllowed   SandboxRefusal = "binary_not_allowed"
	SandboxRefusalPolicy             SandboxRefusal = "policy_refused"
	SandboxRefusalEnvironment        SandboxRefusal = "environment_refused"
)

// SandboxExit is the exit status category of a spawn that ran.
type SandboxExit string

const (
	SandboxExitOK      SandboxExit = "ok"
	SandboxExitNonZero SandboxExit = "non_zero"
	// SandboxExitUnknown: the spawn surface does not report how the command
	// ended.
	SandboxExitUnknown SandboxExit = "unknown"
)

// SandboxPolicySummary is a count-only view of the effective policy of a
// spawn. It names no path.
type SandboxPolicySummary struct {
	Source SandboxPolicySource `json:"source"`
	// Mode and Network are the sandbox policy values ("enforce",
	// "disabled"; "deny", "allow"). Both are empty for SandboxPolicyNone.
	Mode             string `json:"mode,omitempty"`
	Network          string `json:"network,omitempty"`
	EnforceWorkspace bool   `json:"enforce_workspace,omitempty"`
	WriteRoots       int    `json:"write_roots,omitempty"`
	DenyRead         int    `json:"deny_read,omitempty"`
	DenyWrite        int    `json:"deny_write,omitempty"`
}

// SandboxEvidence records one command spawn of a stage: which stage, which
// surface, which program, under which policy and backend, and what happened.
// It never holds the command line, its arguments, or the environment:
// Program is the base name of the first word only, and Validate refuses any
// other shape.
type SandboxEvidence struct {
	Stage     string             `json:"stage"`
	Iteration int                `json:"iteration"`
	Source    SandboxSpawnSource `json:"source"`
	// Program is the base name of the program (for a shell command, the
	// first word of the script). It is "redacted" when that word is not a
	// plain program name.
	Program          string               `json:"program,omitempty"`
	Policy           SandboxPolicySummary `json:"policy"`
	Backend          string               `json:"backend,omitempty"`
	EnforcementLevel string               `json:"enforcement_level,omitempty"`
	Wrapped          bool                 `json:"wrapped"`
	// Escalated marks a command that an approved escalation ran with no
	// sandbox engine.
	Escalated bool `json:"escalated,omitempty"`
	// NestedAccepted marks a spawn that ran unwrapped because Splice already
	// ran inside a sandbox and the user accepted a nested run. Only the
	// outer sandbox confined it. It is never set on a wrapped spawn, a
	// refused spawn, or a spawn with no sandbox policy.
	NestedAccepted bool           `json:"nested_accepted,omitempty"`
	Outcome        SandboxOutcome `json:"outcome"`
	Refusal        SandboxRefusal `json:"refusal,omitempty"`
	Exit           SandboxExit    `json:"exit,omitempty"`
}

// SandboxProgramRedacted is the Program value of a first word that is not a
// plain program name.
const SandboxProgramRedacted = "redacted"

// sandboxProgramPattern is the only shape a recorded program name can have:
// a short base name with no path, no "=", no quote, and no space.
var sandboxProgramPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,31}$`)

// sandboxLabelPattern bounds the backend and level names.
var sandboxLabelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// ValidSandboxProgram reports whether name is a plain program name that the
// evidence may record.
func ValidSandboxProgram(name string) bool {
	return sandboxProgramPattern.MatchString(name)
}

// Validate checks the closed sets, the counts, and the program shape.
func (e SandboxEvidence) Validate() error {
	if e.Stage == "" {
		return errors.New("stage is required")
	}
	if e.Iteration < 0 {
		return errors.New("iteration must be >= 0")
	}
	switch e.Source {
	case SandboxSourceBashTool, SandboxSourceExecCommandTool, SandboxSourceStageCommand, SandboxSourceScannerTool, SandboxSourcePluginTool, SandboxSourceHook, SandboxSourceOther:
	default:
		return fmt.Errorf("invalid source %q", e.Source)
	}
	if e.Program != "" && !ValidSandboxProgram(e.Program) {
		return fmt.Errorf("program %q is not a plain program name", e.Program)
	}
	switch e.Policy.Source {
	case SandboxPolicyStage, SandboxPolicyUser:
	case SandboxPolicyNone:
		if e.Policy.Mode != "" || e.Policy.Network != "" {
			return errors.New("a spawn with no policy has no mode or network")
		}
	default:
		return fmt.Errorf("invalid policy source %q", e.Policy.Source)
	}
	for _, label := range []string{e.Policy.Mode, e.Policy.Network, e.Backend, e.EnforcementLevel} {
		if label != "" && !sandboxLabelPattern.MatchString(label) {
			return fmt.Errorf("invalid sandbox label %q", label)
		}
	}
	if e.Policy.WriteRoots < 0 || e.Policy.DenyRead < 0 || e.Policy.DenyWrite < 0 {
		return errors.New("policy counts must be non-negative")
	}
	if e.NestedAccepted {
		switch {
		case e.Wrapped:
			return errors.New("an accepted nested spawn is not wrapped")
		case e.Policy.Source == SandboxPolicyNone:
			return errors.New("an accepted nested spawn has a sandbox policy")
		case e.Outcome == SandboxOutcomeRefused:
			return errors.New("an accepted nested spawn ran")
		}
	}
	switch e.Outcome {
	case SandboxOutcomeRefused:
		switch e.Refusal {
		case SandboxRefusalNoEnforcingSandbox, SandboxRefusalEscalation, SandboxRefusalPermissionDenied, SandboxRefusalHookBlocked, SandboxRefusalBinaryNotAllowed, SandboxRefusalPolicy, SandboxRefusalEnvironment:
		default:
			return fmt.Errorf("refused spawn has invalid refusal %q", e.Refusal)
		}
		if e.Exit != "" {
			return errors.New("a refused spawn has no exit status")
		}
	case SandboxOutcomeRan, SandboxOutcomeDenied, SandboxOutcomeTimedOut:
		if e.Refusal != "" {
			return fmt.Errorf("outcome %q has refusal %q", e.Outcome, e.Refusal)
		}
		switch e.Exit {
		case SandboxExitOK, SandboxExitNonZero, SandboxExitUnknown:
		default:
			return fmt.Errorf("outcome %q has invalid exit %q", e.Outcome, e.Exit)
		}
	default:
		return fmt.Errorf("invalid outcome %q", e.Outcome)
	}
	return nil
}
