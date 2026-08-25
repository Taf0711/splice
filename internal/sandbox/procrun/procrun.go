// Package procrun funnels child-process creation through one audited seam.
// A caller declares a profile id and a command spec; the runner builds the
// sandbox plan through the existing zeroSandbox engine (or the direct
// unsandboxed path when no engine applies), returns the prepared exec.Cmd,
// and emits one structured audit record per spawn attempt.
//
// Phase 1 adds policy profiles: a request may carry a fixed-binary allowlist
// that the runner enforces before any plan is built, and deterministic
// pipeline callers attach an enforce-mode engine that scopes the filesystem
// to the workspace and denies network.
//
// Deterministic pipeline profiles (splice.stage, splice.dtools) fail closed:
// a spawn without native platform confinement is refused before any command
// is built, and the refusal is audited once.
package procrun

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/secrets"
)

// Profile ids for the shipped call sites. A spawn's audit record names its
// profile so an audit stream can attribute every child process.
const (
	ProfileToolsBash        = "tools.bash"
	ProfileToolsExecCommand = "tools.exec_command"
	ProfileSpliceStage      = "splice.stage"
	ProfileSpliceDTools     = "splice.dtools"
	ProfileHooksDispatch    = "hooks.dispatch"
	ProfileImageInput       = "tui.imageinput"
)

// StageBinaries is the fixed-binary allowlist for deterministic pipeline
// subprocesses: the version-control, build, test, lint, and security scanners
// the stages and dtools invoke by name. Anything outside this set is refused
// loudly. The list is a package constant, not configuration: widening it is a
// reviewed code change, not a runtime decision.
var StageBinaries = []string{
	"bun", "cargo", "false", "git", "go", "gosec", "node", "npm",
	"npx", "pnpm", "python", "python3", "ruff", "tsc", "trivy", "true", "yarn",
}

// NewStageEngine returns an enforce-mode sandbox engine for deterministic
// pipeline subprocesses: the filesystem is scoped to workspaceRoot and the
// network is denied. The engine selects the host native backend with the
// standard sandbox selection interface; a deterministic spawn whose plan
// cannot reach native platform confinement is refused by Prepare, never run
// unwrapped.
func NewStageEngine(workspaceRoot string) *sandbox.Engine {
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	return sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: workspaceRoot,
		Policy: sandbox.Policy{
			Mode:             sandbox.ModeEnforce,
			Network:          sandbox.NetworkDeny,
			EnforceWorkspace: true,
		},
		Backend: backend,
	})
}

// Request is one prepared spawn.
type Request struct {
	// ProfileID names the calling surface, for example ProfileToolsBash.
	ProfileID string
	// Spec carries the binary name, argv, working directory, and optional
	// explicit environment. Nil Env inherits the caller environment on the
	// direct path, exactly as before the runner existed.
	Spec sandbox.CommandSpec
	// Engine is the zeroSandbox engine for this call. Nil means the command
	// runs unsandboxed: an approved escalation or a mode where no engine
	// applies. The runner still audits it. A deterministic profile
	// (splice.stage or splice.dtools) with a nil engine is refused: those
	// spawns require native platform confinement.
	Engine *sandbox.Engine
	// AllowedBinaries restricts Spec.Name to these base names. Nil means
	// unrestricted: the tools profiles gate through permission and sandbox
	// policy instead. A non-empty set makes the runner refuse any other
	// binary loudly, before any plan is built.
	AllowedBinaries []string
}

// Decision records what the engine decided about one spawn.
type Decision struct {
	Backend          string
	TargetBackend    string
	Wrapped          bool
	EnforcementLevel string
	// Rejected is true when the engine refused to build a plan. Reason then
	// carries the refusal message.
	Rejected bool
	Reason   string
}

// AuditRecord is the structured audit line emitted once per Prepare call.
type AuditRecord struct {
	ProfileID string
	Name      string
	Args      []string
	Dir       string
	Decision  Decision
}

// Prepared is the result of a successful Prepare: the command ready to Start,
// its plan, and the audit record that was emitted for it.
type Prepared struct {
	Cmd   *exec.Cmd
	Plan  sandbox.CommandPlan
	Audit AuditRecord
}

var (
	auditMu sync.RWMutex
	// auditSink is nil by default so nothing user-visible changes until a
	// host wires a sink.
	auditSink func(AuditRecord)
)

// SetAuditSink installs the process-wide audit receiver and returns the sink
// it replaced (nil when none was installed). Pass nil to remove the current
// sink. The runner invokes the sink synchronously; receivers must not block.
func SetAuditSink(sink func(AuditRecord)) func(AuditRecord) {
	auditMu.Lock()
	defer auditMu.Unlock()
	previous := auditSink
	auditSink = sink
	return previous
}

func emitAudit(record AuditRecord) {
	auditMu.RLock()
	sink := auditSink
	auditMu.RUnlock()
	if sink != nil {
		sink(record)
	}
}

// Prepare builds the plan and command for req and emits one audit record.
// It returns an error when the binary violates the request's allowlist, when
// a deterministic profile cannot get native platform confinement, or when
// the engine refuses the command; the record emitted in those cases carries
// Rejected with the reason, so a refused spawn is never silent.
func Prepare(ctx context.Context, req Request) (Prepared, error) {
	base := AuditRecord{
		ProfileID: req.ProfileID,
		Name:      req.Spec.Name,
		Args:      append([]string(nil), req.Spec.Args...),
		Dir:       req.Spec.Dir,
	}
	// The fixed-binary allowlist is the first deterministic refusal: a
	// forbidden binary is refused here, before any engine or fail-closed
	// check, so the sandbox never starts and no second audit appears.
	if len(req.AllowedBinaries) > 0 && !binaryAllowed(req.Spec.Name, req.AllowedBinaries) {
		base.Decision.Rejected = true
		base.Decision.Reason = fmt.Sprintf("binary %q is not in the %s profile allowlist", req.Spec.Name, req.ProfileID)
		emitAudit(base)
		return Prepared{}, fmt.Errorf("procrun: refused %s spawn: binary %q is not in the profile allowlist", req.ProfileID, req.Spec.Name)
	}
	// A deterministic profile must have a sandbox engine. Refuse before any
	// process object exists so an unwrapped stage spawn is impossible.
	if deterministicProfile(req.ProfileID) && req.Engine == nil {
		reason := fmt.Sprintf("deterministic profile %s requires a sandbox engine", req.ProfileID)
		base.Decision = Decision{Rejected: true, Reason: reason}
		emitAudit(base)
		return Prepared{}, fmt.Errorf("procrun: refused %s spawn: %s", req.ProfileID, reason)
	}
	if req.Engine != nil {
		command, plan, err := req.Engine.CommandContext(ctx, req.Spec)
		record := base
		record.Decision = decisionFromPlan(plan)
		if err != nil {
			record.Decision.Rejected = true
			record.Decision.Reason = err.Error()
			emitAudit(record)
			return Prepared{}, err
		}
		// The deterministic guard asserts confinement positively: a
		// deterministic spawn is accepted only when the plan is wrapped and
		// natively enforced. It deliberately does not trust
		// plan.RequiresPlatformSandbox as the trigger, because the bypass
		// conditions control that field: an inherited SPLICE_SANDBOXED marker
		// (with SPLICE_SANDBOX_BACKEND set) or a disabled policy mode flips
		// the manager to SandboxPreferenceForbid and zeroes it. An inherited
		// marker does not prove the current spawn is confined, so a
		// deterministic spawn under one is refused. No environment variable
		// and no policy mode can make a deterministic spawn run without a
		// wrapped native plan.
		if deterministicProfile(req.ProfileID) &&
			(!plan.Wrapped || plan.EnforcementLevel != sandbox.EnforcementNative) {
			reason := deterministicRefusalReason(req.ProfileID, plan)
			record.Decision.Rejected = true
			record.Decision.Reason = reason
			emitAudit(record)
			return Prepared{}, fmt.Errorf("procrun: refused %s spawn: %s", req.ProfileID, reason)
		}
		emitAudit(record)
		return Prepared{Cmd: command, Plan: plan, Audit: record}, nil
	}
	// Direct unsandboxed path. This mirrors the construction the migrated
	// call sites performed before the runner existed: a pass-through plan
	// naming the unavailable backend, inherited-and-scrubbed environment, no
	// wrapper.
	plan := sandbox.CommandPlan{
		Backend: sandbox.Backend{
			Name:    sandbox.BackendUnavailable,
			Message: "sandbox engine not provided",
		},
		Wrapped: false,
		Name:    req.Spec.Name,
		Args:    req.Spec.Args,
		Dir:     req.Spec.Dir,
	}
	command := exec.CommandContext(ctx, req.Spec.Name, req.Spec.Args...)
	command.Dir = req.Spec.Dir
	if req.Spec.Env != nil {
		// An explicit environment travels verbatim. Callers that need the
		// process environment apply their own rules first (see the hooks
		// dispatcher, which scrubs and then appends hook-specific entries).
		command.Env = req.Spec.Env
	} else {
		// Escalated (non-sandboxed) commands inherit the process env by default.
		// Scrub credential-bearing variables so the child cannot exfiltrate keys.
		command.Env = secrets.ScrubChildEnv(os.Environ())
	}
	emitAudit(base)
	return Prepared{Cmd: command, Plan: plan, Audit: base}, nil
}

func decisionFromPlan(plan sandbox.CommandPlan) Decision {
	return Decision{
		Backend:          string(plan.Backend.Name),
		TargetBackend:    string(plan.TargetBackend),
		Wrapped:          plan.Wrapped,
		EnforcementLevel: string(plan.EnforcementLevel),
	}
}

// deterministicProfile reports whether a profile id is a deterministic
// pipeline profile. These spawns require native platform confinement and are
// refused when it is unavailable.
func deterministicProfile(profileID string) bool {
	return profileID == ProfileSpliceStage || profileID == ProfileSpliceDTools
}

// deterministicRefusalReason names the profile, backend, wrapped state, and
// enforcement level of a refused deterministic spawn. The same text travels
// on the returned error and the audit decision.
func deterministicRefusalReason(profileID string, plan sandbox.CommandPlan) string {
	return fmt.Sprintf(
		"deterministic profile %s requires native platform confinement: backend=%s wrapped=%t enforcement=%s",
		profileID, plan.Backend.Name, plan.Wrapped, plan.EnforcementLevel)
}

// binaryAllowed matches the command's base name against the allowlist. Stage
// call sites name runners by bare name or by a resolved path (for example a
// workspace-local node_modules/.bin/tsc), so the base name is the stable
// identity to gate on.
func binaryAllowed(name string, allowed []string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(name)))
	if base == "" || base == "." {
		return false
	}
	for _, candidate := range allowed {
		if base == strings.ToLower(strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}
