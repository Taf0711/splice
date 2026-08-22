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
// network is denied. Platform backends degrade per their own rules; the
// policy metadata travels on every plan either way.
func NewStageEngine(workspaceRoot string) *sandbox.Engine {
	return sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: workspaceRoot,
		Policy: sandbox.Policy{
			Mode:             sandbox.ModeEnforce,
			Network:          sandbox.NetworkDeny,
			EnforceWorkspace: true,
		},
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
	// applies. The runner still audits it.
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
// It returns an error when the binary violates the request's allowlist or the
// engine refuses the command; the record emitted in those cases carries
// Rejected with the reason, so a refused spawn is never silent.
func Prepare(ctx context.Context, req Request) (Prepared, error) {
	base := AuditRecord{
		ProfileID: req.ProfileID,
		Name:      req.Spec.Name,
		Args:      append([]string(nil), req.Spec.Args...),
		Dir:       req.Spec.Dir,
	}
	if len(req.AllowedBinaries) > 0 && !binaryAllowed(req.Spec.Name, req.AllowedBinaries) {
		base.Decision.Rejected = true
		base.Decision.Reason = fmt.Sprintf("binary %q is not in the %s profile allowlist", req.Spec.Name, req.ProfileID)
		emitAudit(base)
		return Prepared{}, fmt.Errorf("procrun: refused %s spawn: binary %q is not in the profile allowlist", req.ProfileID, req.Spec.Name)
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
