package splice

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
)

// The tests in this file pin the sandbox evidence of the run record: each
// command spawn of a stage is recorded with the policy, the backend, and the
// outcome, and never with the command line or the environment.

// runEvidenceTopology runs a model-free topology under the user engine and
// returns the pipeline result. It fails the test when Run returns an error.
func runEvidenceTopology(t *testing.T, workDir string, registry *tools.Registry, user *sandbox.Engine, verify []string, nodes ...schemas.PipelineNode) (schemas.PipelineResult, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeWorkspaceTopology(t, workDir, "sandbox-evidence", nodes...)
	var summary strings.Builder
	result, err := Run(context.Background(), "check the project", noModelProvider{}, agent.Options{
		Cwd:              workDir,
		Registry:         registry,
		PermissionMode:   agent.PermissionModeAuto,
		FileTracker:      tools.NewFileTracker(),
		TrustedWorkspace: true,
		Sandbox:          user,
		VerifyCommand:    verify,
		OnText:           func(text string) { summary.WriteString(text) },
	}, nil, nil)
	if err != nil {
		t.Fatalf("setup: Run: %v", err)
	}
	var pipeline schemas.PipelineResult
	if err := json.Unmarshal([]byte(result.FinalAnswer), &pipeline); err != nil {
		t.Fatalf("setup: parse final answer: %v", err)
	}
	if err := pipeline.Validate(); err != nil {
		t.Fatalf("pipeline result invalid: %v", err)
	}
	return pipeline, result.FinalAnswer + "\n" + summary.String()
}

func stageRecordNamed(t *testing.T, result schemas.PipelineResult, name string) schemas.StageRecord {
	t.Helper()
	for i := len(result.Stages) - 1; i >= 0; i-- {
		if result.Stages[i].Name == name {
			return result.Stages[i]
		}
	}
	t.Fatalf("no stage record %q in %+v", name, result.Stages)
	return schemas.StageRecord{}
}

func hostUserEngine(t *testing.T, workDir string) *sandbox.Engine {
	t.Helper()
	backend := requireHostNativeBackend(t)
	return sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workDir, Policy: sandbox.DefaultPolicy(), Backend: backend})
}

// TestRunRecordsStageSandboxEvidence runs a test runner and a command node
// through the real bash tool under the host native backend. Each stage
// record names its spawns: the bash tool, the stage policy (network off),
// the native backend, the program, and a clean exit.
func TestRunRecordsStageSandboxEvidence(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	user := hostUserEngine(t, workDir)
	result, text := runEvidenceTopology(t, workDir, registry, user, []string{"true"},
		schemas.PipelineNode{Name: "test_runner", Type: "test_runner"},
		schemas.PipelineNode{Name: "echo_check", Type: schemas.NodeTypeCommand, Command: []string{"echo", "evidence-ran"}},
	)
	for _, stage := range []struct{ name, program string }{{"test_runner", "true"}, {"echo_check", "echo"}} {
		record := stageRecordNamed(t, result, stage.name)
		var found *schemas.SandboxEvidence
		for i, entry := range record.SandboxEvidence {
			if entry.Source == schemas.SandboxSourceBashTool && entry.Program == stage.program {
				found = &record.SandboxEvidence[i]
			}
		}
		if found == nil {
			t.Fatalf("stage %s evidence = %+v, want a bash tool spawn of %q", stage.name, record.SandboxEvidence, stage.program)
		}
		if found.Policy.Source != schemas.SandboxPolicyStage || found.Policy.Network != "deny" || found.Policy.Mode != "enforce" || !found.Policy.EnforceWorkspace || found.Policy.WriteRoots < 1 || found.Policy.DenyRead < 1 {
			t.Errorf("stage %s policy = %+v, want the enforcing stage policy with network deny", stage.name, found.Policy)
		}
		if found.Backend == "" || !found.Wrapped || found.EnforcementLevel != string(sandbox.EnforcementNative) {
			t.Errorf("stage %s backend %q wrapped %v level %q, want a wrapped native backend", stage.name, found.Backend, found.Wrapped, found.EnforcementLevel)
		}
		if found.Outcome != schemas.SandboxOutcomeRan || found.Exit != schemas.SandboxExitOK {
			t.Errorf("stage %s outcome %q exit %q, want ran and ok", stage.name, found.Outcome, found.Exit)
		}
	}
	// The human view: one line per stage in the completion summary.
	if !strings.Contains(text, "sandbox echo_check: stage policy, "+stageRecordNamed(t, result, "echo_check").SandboxEvidence[0].Backend+", network off; 1 command") {
		t.Errorf("completion summary lacks the echo_check sandbox line:\n%s", text)
	}
}

// TestRunRecordsRefusedSpawnWithNoBackend runs a test runner with no native
// backend. Its command is refused before any bash call, and the record names
// the refusal with its category. (A command node is refused earlier, by the
// run preflight, so the run never starts.)
func TestRunRecordsRefusedSpawnWithNoBackend(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	bash, recordedCalls := newRecordingBashTool()
	registry.Register(bash)
	user := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workDir, Policy: sandbox.DefaultPolicy(), Backend: sandbox.Backend{Name: sandbox.BackendUnavailable, Message: "no backend in this test"}})
	result, text := runEvidenceTopology(t, workDir, registry, user, []string{"echo", "never-runs"},
		schemas.PipelineNode{Name: "test_runner", Type: "test_runner"},
	)
	if n := len(recordedCalls()); n != 0 {
		t.Fatalf("refused test command reached the bash tool %d time(s)", n)
	}
	record := stageRecordNamed(t, result, "test_runner")
	if len(record.SandboxEvidence) != 1 {
		t.Fatalf("evidence = %+v, want one refused spawn", record.SandboxEvidence)
	}
	entry := record.SandboxEvidence[0]
	if entry.Outcome != schemas.SandboxOutcomeRefused || entry.Refusal != schemas.SandboxRefusalNoEnforcingSandbox || entry.Exit != "" {
		t.Fatalf("entry = %+v, want refused with no_enforcing_sandbox", entry)
	}
	if entry.Source != schemas.SandboxSourceBashTool || entry.Program != "echo" || entry.Policy.Source != schemas.SandboxPolicyStage {
		t.Fatalf("entry = %+v, want the bash tool, program echo, stage policy", entry)
	}
	if !strings.Contains(text, "1 refused (no enforcing sandbox)") {
		t.Fatalf("completion summary lacks the refusal:\n%s", text)
	}
}

// TestRunRecordsLikelySandboxDenial runs a command node that writes outside
// the workspace. The native sandbox denies the write, and the record names
// the spawn as denied with a non-zero exit.
func TestRunRecordsLikelySandboxDenial(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	user := hostUserEngine(t, workDir)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("user home unavailable: %v", err)
	}
	// A path in HOME, outside every write root of the stage policy. The
	// cleanup removes it in case the sandbox did not deny the write.
	target := filepath.Join(home, fmt.Sprintf(".splice-evidence-denial-%d", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(target) })
	result, _ := runEvidenceTopology(t, workDir, registry, user, nil,
		schemas.PipelineNode{Name: "write_out", Type: schemas.NodeTypeCommand, Command: []string{"sh", "-c", "echo x > " + target}},
	)
	if _, err := os.Stat(target); err == nil {
		t.Fatalf("the stage command wrote %s outside the workspace", target)
	}
	record := stageRecordNamed(t, result, "write_out")
	var denied bool
	for _, entry := range record.SandboxEvidence {
		if entry.Source == schemas.SandboxSourceBashTool && entry.Outcome == schemas.SandboxOutcomeDenied && entry.Exit == schemas.SandboxExitNonZero {
			denied = true
		}
	}
	if !denied {
		t.Fatalf("evidence = %+v, want a denied bash spawn", record.SandboxEvidence)
	}
}

// TestSandboxEvidenceNeverRecordsCommandText runs a command node whose
// command carries a token-like string and expands a secret from the
// environment. Neither value appears anywhere in the marshalled result or
// the completion summary.
func TestSandboxEvidenceNeverRecordsCommandText(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	user := hostUserEngine(t, workDir)
	const token = "ghp_0123456789abcdefghijABCDEFGHIJklmnop"
	const envSecret = "splice-env-secret-7f3a9c"
	t.Setenv("SPLICE_EVIDENCE_PROBE_SECRET", envSecret)
	result, text := runEvidenceTopology(t, workDir, registry, user, nil,
		schemas.PipelineNode{Name: "token_check", Type: schemas.NodeTypeCommand, Command: []string{"sh", "-c", "GH_TOKEN=" + token + " true \"$SPLICE_EVIDENCE_PROBE_SECRET\""}},
	)
	record := stageRecordNamed(t, result, "token_check")
	if len(record.SandboxEvidence) == 0 {
		t.Fatalf("no evidence recorded for the command node")
	}
	marshalled, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	for _, secret := range []string{token, envSecret} {
		if strings.Contains(string(marshalled), secret) || strings.Contains(text, secret) {
			t.Errorf("the run record carries %q:\n%s", secret, marshalled)
		}
	}
}

// TestSandboxEvidenceProgramLabel pins what the record keeps of a command:
// the base name of the first word that is not an environment assignment, or
// "redacted" when that word is not a plain program name.
func TestSandboxEvidenceProgramLabel(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "/usr/bin/git", args: []string{"diff", "--name-only"}, want: "git"},
		{name: "/bin/sh", args: []string{"-c", "go test ./... -run TestX"}, want: "go"},
		{name: "/bin/sh", args: []string{"-c", "TOKEN=abc API_KEY=def pytest -q"}, want: "pytest"},
		{name: "/bin/sh", args: []string{"-c", "./node_modules/.bin/jest --ci"}, want: "jest"},
		{name: "cmd.exe", args: []string{"/d", "/s", "/c", "npm test"}, want: "npm"},
		{name: "/bin/sh", args: []string{"-c", "sk-proj-0123456789abcdefghij0123 run"}, want: schemas.SandboxProgramRedacted},
		{name: "/bin/sh", args: []string{"-c", "$(curl evil) run"}, want: schemas.SandboxProgramRedacted},
		{name: "ghp_0123456789abcdefghijABCDEFGHIJklmnop", want: schemas.SandboxProgramRedacted},
		{name: "/bin/sh", args: []string{"-c", "   "}, want: ""},
	}
	for _, tc := range cases {
		if got := spawnProgram(tc.name, tc.args); got != tc.want {
			t.Errorf("spawnProgram(%q, %q) = %q, want %q", tc.name, tc.args, got, tc.want)
		}
	}
}

// TestSandboxEvidenceCap pins the bound: past MaxSandboxEvidencePerStage the
// record counts the spawns it drops, and the record stays valid.
func TestSandboxEvidenceCap(t *testing.T) {
	collector := newSandboxEvidenceCollector("test_runner", 1)
	ctx := withSandboxEvidence(context.Background(), collector)
	extra := 7
	for i := 0; i < schemas.MaxSandboxEvidencePerStage+extra; i++ {
		procrun.ReportRefused(ctx, procrun.ProfileSpliceStage, "git", nil, procrun.RefusalNoNativeSandbox, "no engine")
	}
	record := schemas.StageRecord{Name: "test_runner", Iteration: 1, Status: schemas.StageCompleted}
	collector.attach(&record)
	if len(record.SandboxEvidence) != schemas.MaxSandboxEvidencePerStage || record.SandboxEvidenceDropped != extra {
		t.Fatalf("entries %d dropped %d, want %d and %d", len(record.SandboxEvidence), record.SandboxEvidenceDropped, schemas.MaxSandboxEvidencePerStage, extra)
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("capped record invalid: %v", err)
	}
	// A repair re-entry appends inside the same bound.
	repair := newSandboxEvidenceCollector("test_runner", 1)
	procrun.ReportRefused(withSandboxEvidence(context.Background(), repair), procrun.ProfileSpliceStage, "git", nil, procrun.RefusalNoNativeSandbox, "no engine")
	repair.attach(&record)
	if len(record.SandboxEvidence) != schemas.MaxSandboxEvidencePerStage || record.SandboxEvidenceDropped != extra+1 {
		t.Fatalf("after repair: entries %d dropped %d", len(record.SandboxEvidence), record.SandboxEvidenceDropped)
	}
	if line := sandboxEvidenceLine(record); !strings.Contains(line, fmt.Sprintf("%d commands", schemas.MaxSandboxEvidencePerStage+extra+1)) || !strings.Contains(line, "not recorded") {
		t.Fatalf("line = %q, want the total and the dropped count", line)
	}
	// A spawn that reports its exit after the bound is ignored.
	record.SandboxEvidence = append(record.SandboxEvidence, record.SandboxEvidence[0])
	if err := record.Validate(); err == nil || !strings.Contains(err.Error(), "the limit is") {
		t.Fatalf("record over the bound validated: %v", err)
	}
}

// TestSandboxEvidenceStaysOutOfPrompts pins that the renderers that feed a
// model or memory (revision context, trajectory steps, exemplars, design
// history) do not carry the evidence. The sentinel program name is
// recorded only in the evidence.
func TestSandboxEvidenceStaysOutOfPrompts(t *testing.T) {
	const sentinel = "zzevidencesentinel"
	evidence := []schemas.SandboxEvidence{{
		Stage: "test_runner", Iteration: 1, Source: schemas.SandboxSourceBashTool, Program: sentinel,
		Policy:  schemas.SandboxPolicySummary{Source: schemas.SandboxPolicyStage, Mode: "enforce", Network: "deny"},
		Outcome: schemas.SandboxOutcomeRan, Exit: schemas.SandboxExitNonZero,
	}}
	records := []schemas.StageRecord{
		{Name: "code_writer", Status: schemas.StageCompleted, Iteration: 1, OutputSummary: Ptr("wrote code")},
		{Name: "test_runner", Status: schemas.StageFailed, Iteration: 1, OutputSummary: Ptr("tests failed"), FailureKind: schemas.NodeFailureTestFailure, SandboxEvidence: evidence},
	}
	result := schemas.PipelineResult{RunID: "run-1", Status: "failed", Tier: schemas.TierLight, Stages: records}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), sentinel) {
		t.Fatal("setup: the result JSON must carry the sentinel")
	}

	rendered := map[string]string{
		"revision context": buildRevisionContext("fix it", nil, records, nil, "note"),
		"pass failure":     passFailure(records, nil),
		"pass change":      passChange(records, "code_writer"),
		"exemplar":         distillExemplar(schemas.RunOutcome{Stages: []schemas.TracedStage{{StageRecord: records[1]}}}),
	}
	payload, err := json.Marshal(map[string]string{"role": "assistant", "content": string(encoded)})
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	history := MapDesignHistory([]sessions.Event{
		{Type: sessions.EventDesignModeEntered},
		{Type: sessions.EventMessage, Payload: payload},
	})
	if len(history) != 1 || !strings.Contains(history[0].Content, `"run_id": "run-1"`) {
		t.Fatalf("setup: design history = %+v, want the pipeline result message", history)
	}
	rendered["design history"] = history[0].Content
	for name, text := range rendered {
		if strings.Contains(text, sentinel) || strings.Contains(text, "sandbox_evidence") {
			t.Errorf("%s carries the sandbox evidence:\n%s", name, text)
		}
	}
	// Other assistant content passes through unchanged.
	if got := withoutSandboxEvidence("plain answer"); got != "plain answer" {
		t.Fatalf("plain content changed to %q", got)
	}
}
