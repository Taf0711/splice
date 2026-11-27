package splice

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"sync"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/secrets"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
)

// sandboxEvidenceCollector records the command spawns of one stage
// invocation. It is the procrun spawn observer of the stage context, so
// every procrun spawn under the stage (registry tools, stage commands,
// scanners, plugin tools, hooks, agent node tools) reaches it. The tool
// runners add the calls they refuse before any spawn.
//
// It keeps the program base name only, never the command line, the
// arguments, or the environment.
type sandboxEvidenceCollector struct {
	mu        sync.Mutex
	stage     string
	iteration int
	entries   []schemas.SandboxEvidence
	dropped   int
	byCmd     map[*exec.Cmd]int
	// nestedAccepted marks a run whose user accepted a nested sandbox
	// (PipelineRunConfig.nestedSandboxAccepted). An unwrapped spawn under a
	// sandbox policy is then recorded as NestedAccepted.
	nestedAccepted bool
}

func newSandboxEvidenceCollector(stage string, iteration int) *sandboxEvidenceCollector {
	return &sandboxEvidenceCollector{stage: stage, iteration: iteration, byCmd: map[*exec.Cmd]int{}}
}

type sandboxEvidenceKey struct{}

// withSandboxEvidence returns ctx with the collector as its spawn observer.
func withSandboxEvidence(ctx context.Context, collector *sandboxEvidenceCollector) context.Context {
	ctx = procrun.WithSpawnObserver(ctx, collector)
	return context.WithValue(ctx, sandboxEvidenceKey{}, collector)
}

func sandboxEvidenceFrom(ctx context.Context) *sandboxEvidenceCollector {
	collector, _ := ctx.Value(sandboxEvidenceKey{}).(*sandboxEvidenceCollector)
	return collector
}

// add appends one entry and returns its index, or -1 past the bound.
func (c *sandboxEvidenceCollector) add(entry schemas.SandboxEvidence) int {
	entry.Stage, entry.Iteration = c.stage, c.iteration
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= schemas.MaxSandboxEvidencePerStage {
		c.dropped++
		return -1
	}
	c.entries = append(c.entries, entry)
	return len(c.entries) - 1
}

// ObserveSpawn records one procrun spawn attempt.
func (c *sandboxEvidenceCollector) ObserveSpawn(spawn procrun.Spawn) {
	decision := spawn.Audit.Decision
	entry := schemas.SandboxEvidence{
		Source:           spawnSource(spawn.Audit.ProfileID),
		Program:          spawnProgram(spawn.Audit.Name, spawn.Audit.Args),
		Policy:           policySummary(spawn.Policy),
		Backend:          sandboxLabel(decision.Backend),
		EnforcementLevel: sandboxLabel(decision.EnforcementLevel),
		Wrapped:          decision.Wrapped,
		Outcome:          schemas.SandboxOutcomeRan,
		Exit:             schemas.SandboxExitUnknown,
	}
	if decision.Rejected {
		entry.Outcome, entry.Refusal, entry.Exit = schemas.SandboxOutcomeRefused, refusalFromProcrun(decision.Refusal), ""
	} else if c.nestedAccepted && !decision.Wrapped && entry.Policy.Source != schemas.SandboxPolicyNone {
		entry.NestedAccepted = true
	}
	index := c.add(entry)
	if index >= 0 && spawn.Cmd != nil && !decision.Rejected {
		c.mu.Lock()
		c.byCmd[spawn.Cmd] = index
		c.mu.Unlock()
	}
}

// ObserveExit records how a spawned command ended.
func (c *sandboxEvidenceCollector) ObserveExit(cmd *exec.Cmd, exit procrun.SpawnExit) {
	c.mu.Lock()
	defer c.mu.Unlock()
	index, ok := c.byCmd[cmd]
	if !ok {
		return
	}
	delete(c.byCmd, cmd)
	entry := &c.entries[index]
	entry.Escalated = exit.Escalated
	if exit.Refused != "" {
		entry.Outcome, entry.Refusal, entry.Exit = schemas.SandboxOutcomeRefused, refusalFromProcrun(exit.Refused), ""
		entry.NestedAccepted = false
		return
	}
	entry.Exit = schemas.SandboxExitOK
	if exit.ExitCode != 0 {
		entry.Exit = schemas.SandboxExitNonZero
	}
	switch {
	case exit.TimedOut:
		entry.Outcome, entry.Exit = schemas.SandboxOutcomeTimedOut, schemas.SandboxExitNonZero
	case exit.LikelyDenied:
		entry.Outcome = schemas.SandboxOutcomeDenied
	}
}

// refuseToolCall records a registry tool call that a runner refused before
// any spawn. Only a tool that runs commands (shell side effect) is recorded:
// a refused file read is not a spawn.
func (c *sandboxEvidenceCollector) refuseToolCall(registry *tools.Registry, name string, args map[string]any, engine *sandbox.Engine, refusal schemas.SandboxRefusal) {
	if c == nil || registry == nil {
		return
	}
	tool, ok := registry.Get(name)
	if !ok || tool.Safety().SideEffect != tools.SideEffectShell {
		return
	}
	program := ""
	if command, ok := args["command"].(string); ok {
		program = scriptProgram(command)
	}
	c.add(schemas.SandboxEvidence{
		Source:  toolSource(name),
		Program: program,
		Policy:  policySummary(engine.PolicySummary()),
		Outcome: schemas.SandboxOutcomeRefused,
		Refusal: refusal,
	})
}

// snapshot returns a copy of the entries and the dropped count.
func (c *sandboxEvidenceCollector) snapshot() ([]schemas.SandboxEvidence, int) {
	if c == nil {
		return nil, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]schemas.SandboxEvidence(nil), c.entries...), c.dropped
}

// attach stores the collected evidence on a stage record. A repair re-entry
// of the same stage appends to the record, inside the same bound.
func (c *sandboxEvidenceCollector) attach(record *schemas.StageRecord) {
	entries, dropped := c.snapshot()
	appendSandboxEvidence(record, entries, dropped)
}

func appendSandboxEvidence(record *schemas.StageRecord, entries []schemas.SandboxEvidence, dropped int) {
	for _, entry := range entries {
		if len(record.SandboxEvidence) >= schemas.MaxSandboxEvidencePerStage {
			record.SandboxEvidenceDropped++
			continue
		}
		entry.Stage, entry.Iteration = record.Name, record.Iteration
		record.SandboxEvidence = append(record.SandboxEvidence, entry)
	}
	record.SandboxEvidenceDropped += dropped
}

func spawnSource(profileID string) schemas.SandboxSpawnSource {
	switch profileID {
	case procrun.ProfileToolsBash:
		return schemas.SandboxSourceBashTool
	case procrun.ProfileToolsExecCommand:
		return schemas.SandboxSourceExecCommandTool
	case procrun.ProfileSpliceStage:
		return schemas.SandboxSourceStageCommand
	case procrun.ProfileSpliceDTools:
		return schemas.SandboxSourceScannerTool
	case procrun.ProfilePluginTool:
		return schemas.SandboxSourcePluginTool
	case procrun.ProfileHooksDispatch:
		return schemas.SandboxSourceHook
	default:
		return schemas.SandboxSourceOther
	}
}

func toolSource(name string) schemas.SandboxSpawnSource {
	switch name {
	case "bash":
		return schemas.SandboxSourceBashTool
	case "exec_command":
		return schemas.SandboxSourceExecCommandTool
	case "bandit", "gosec", "sarif":
		return schemas.SandboxSourceScannerTool
	default:
		return schemas.SandboxSourceOther
	}
}

func refusalFromProcrun(kind procrun.RefusalKind) schemas.SandboxRefusal {
	switch kind {
	case procrun.RefusalBinaryNotAllowed:
		return schemas.SandboxRefusalBinaryNotAllowed
	case procrun.RefusalNoNativeSandbox:
		return schemas.SandboxRefusalNoEnforcingSandbox
	case procrun.RefusalEnvironment:
		return schemas.SandboxRefusalEnvironment
	default:
		return schemas.SandboxRefusalPolicy
	}
}

// refusalFromDenial maps an agent loop denial (always before the call ran).
func refusalFromDenial(denial agent.DenialCategory) schemas.SandboxRefusal {
	switch denial {
	case agent.DenialHookBlocked:
		return schemas.SandboxRefusalHookBlocked
	case agent.DenialSandboxBlock, agent.DenialSandboxDenied, agent.DenialPolicy:
		return schemas.SandboxRefusalPolicy
	default:
		return schemas.SandboxRefusalPermissionDenied
	}
}

func policySummary(summary sandbox.PolicySummary) schemas.SandboxPolicySummary {
	if summary.Mode == "" {
		return schemas.SandboxPolicySummary{Source: schemas.SandboxPolicyNone}
	}
	source := schemas.SandboxPolicyUser
	if summary.Stage {
		source = schemas.SandboxPolicyStage
	}
	return schemas.SandboxPolicySummary{
		Source:           source,
		Mode:             sandboxLabel(string(summary.Mode)),
		Network:          sandboxLabel(string(summary.Network)),
		EnforceWorkspace: summary.EnforceWorkspace,
		WriteRoots:       summary.WriteRoots,
		DenyRead:         summary.DenyRead,
		DenyWrite:        summary.DenyWrite,
	}
}

// sandboxLabel keeps a backend, level, mode, or network name only when it
// has the plain shape of one.
func sandboxLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 40 {
		return ""
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return ""
		}
	}
	return value
}

// shellPrograms are the shells whose script argument names the program.
var shellPrograms = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "cmd": true, "cmd.exe": true}

// spawnProgram returns the program label of a spawn. For a shell with a
// script argument (-c, /c) it is the first word of the script. The rest of
// the command line is never kept.
func spawnProgram(name string, args []string) string {
	program := baseName(name)
	if shellPrograms[strings.ToLower(program)] {
		for i, arg := range args {
			if (arg == "-c" || strings.EqualFold(arg, "/c")) && i+1 < len(args) {
				return scriptProgram(args[i+1])
			}
		}
	}
	return programLabel(program)
}

// scriptProgram returns the label of the first word of a shell script that
// is not an environment assignment (NAME=value).
func scriptProgram(script string) string {
	for _, word := range strings.Fields(script) {
		word = strings.Trim(word, `"'`)
		if eq := strings.IndexByte(word, '='); eq > 0 {
			continue
		}
		return programLabel(baseName(word))
	}
	return ""
}

func baseName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, `\`, "/"))
	if name == "" {
		return ""
	}
	return path.Base(name)
}

// programLabel keeps a word only when it is a plain program name with no
// secret pattern in it. Any other word is recorded as "redacted".
func programLabel(word string) string {
	if word == "" {
		return ""
	}
	if !schemas.ValidSandboxProgram(word) || len(secrets.Scan(word)) > 0 {
		return schemas.SandboxProgramRedacted
	}
	return word
}

// sandboxEvidenceLine is the one-line human view of a stage's sandbox
// evidence, for example "sandbox test_runner: stage policy, macos-seatbelt,
// network off; 3 commands; 1 refused (no enforcing sandbox)". A stage that
// spawned nothing has no line.
func sandboxEvidenceLine(record schemas.StageRecord) string {
	total := len(record.SandboxEvidence) + record.SandboxEvidenceDropped
	if total == 0 {
		return ""
	}
	var policies, backends, networks, refusals []string
	refused, denied, timedOut, failed, escalated, nested := 0, 0, 0, 0, 0, 0
	for _, entry := range record.SandboxEvidence {
		policy := string(entry.Policy.Source) + " policy"
		if entry.Policy.Source == schemas.SandboxPolicyNone {
			policy = "no sandbox policy"
		}
		policies = appendDistinct(policies, policy)
		if entry.Backend != "" {
			backends = appendDistinct(backends, entry.Backend)
		}
		switch entry.Policy.Network {
		case "deny":
			networks = appendDistinct(networks, "network off")
		case "allow":
			networks = appendDistinct(networks, "network on")
		}
		switch entry.Outcome {
		case schemas.SandboxOutcomeRefused:
			refused++
			refusals = appendDistinct(refusals, strings.ReplaceAll(string(entry.Refusal), "_", " "))
		case schemas.SandboxOutcomeDenied:
			denied++
		case schemas.SandboxOutcomeTimedOut:
			timedOut++
		default:
			if entry.Exit == schemas.SandboxExitNonZero {
				failed++
			}
		}
		if entry.Escalated {
			escalated++
		}
		if entry.NestedAccepted {
			nested++
		}
	}
	head := append(append(policies, backends...), networks...)
	parts := []string{strings.Join(head, ", "), pluralDetail(total, "command")}
	if refused > 0 {
		parts = append(parts, fmt.Sprintf("%d refused (%s)", refused, strings.Join(refusals, ", ")))
	}
	if denied > 0 {
		parts = append(parts, fmt.Sprintf("%d denied by the sandbox", denied))
	}
	if timedOut > 0 {
		parts = append(parts, fmt.Sprintf("%d timed out", timedOut))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d exited non-zero", failed))
	}
	if escalated > 0 {
		parts = append(parts, fmt.Sprintf("%d escalated to run with no sandbox", escalated))
	}
	if nested > 0 {
		parts = append(parts, fmt.Sprintf("%d not wrapped (nested run accepted by the user)", nested))
	}
	if record.SandboxEvidenceDropped > 0 {
		parts = append(parts, fmt.Sprintf("%d not recorded (limit %d)", record.SandboxEvidenceDropped, schemas.MaxSandboxEvidencePerStage))
	}
	name := record.Name
	if record.Iteration > 1 {
		name += fmt.Sprintf(" (pass %d)", record.Iteration)
	}
	return "sandbox " + name + ": " + strings.Join(parts, "; ")
}

func appendDistinct(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// withoutSandboxEvidence returns a pipeline result JSON with the sandbox
// evidence removed. Conversation history that feeds a model uses it: the
// evidence is for the user and for audit, never for a prompt. Any other
// content returns unchanged.
func withoutSandboxEvidence(content string) string {
	if !strings.Contains(content, `"sandbox_evidence`) {
		return content
	}
	var result schemas.PipelineResult
	if err := json.Unmarshal([]byte(content), &result); err != nil || result.Status == "" {
		return content
	}
	for i := range result.Stages {
		result.Stages[i].SandboxEvidence = nil
		result.Stages[i].SandboxEvidenceDropped = 0
	}
	stripped, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return content
	}
	return string(stripped)
}
