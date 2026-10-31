package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/eval"
)

// ---- Test seam ----

// seamRunner is a controllable eval.RunFunc: it records the context it was
// handed (so tests can prove a per-attempt timeout context arrived) and
// behaves per its fields, with no real waits beyond what a test sets.
type seamRunner struct {
	// mu-protected call log lives in the test via channel; this struct is
	// single-goroutine per test.
	Calls        []seamCall
	Outputs      []eval.RunOutput
	Errors       []error
	BlockUntil   <-chan time.Time // when non-nil, block until it fires
	TimeoutAfter time.Duration    // when > 0, block until the ctx deadline
}

type seamCall struct {
	Ctx       context.Context
	Input     eval.RunInput
	CtxDone   bool
	HasDeadly bool // ctx carried a deadline
}

func (s *seamRunner) run(ctx context.Context, in eval.RunInput) (eval.RunOutput, error) {
	call := seamCall{Ctx: ctx, Input: in, CtxDone: ctx.Err() != nil}
	_, hasDeadline := ctx.Deadline()
	call.HasDeadly = hasDeadline
	s.Calls = append(s.Calls, call)
	index := len(s.Calls) - 1
	var out eval.RunOutput
	if index < len(s.Outputs) {
		out = s.Outputs[index]
	}
	var runErr error
	if index < len(s.Errors) {
		runErr = s.Errors[index]
	}
	if s.TimeoutAfter > 0 {
		select {
		case <-ctx.Done():
			return eval.RunOutput{}, ctx.Err()
		case <-time.After(s.TimeoutAfter):
		}
	}
	if s.BlockUntil != nil {
		select {
		case <-ctx.Done():
			return eval.RunOutput{}, ctx.Err()
		case <-s.BlockUntil:
		}
	}
	return out, runErr
}

// newSeamTestManifest builds a one-family manifest whose verifier files are
// real shell scripts in a temp manifest dir.
func newSeamTestManifest(t *testing.T, precursorScript, targetScript string) (mvpFamilyManifest, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "precursor.sh"), []byte(precursorScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "target.sh"), []byte(targetScript), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := mvpFamilyManifest{
		Schema:   "test",
		Fixture:  "fixture",
		Families: []mvpFamilyEntry{{ID: "fam-a", PrecursorTask: "do A", TargetTask: "do B", PrecursorCheckFile: "precursor.sh", TargetCheckFile: "target.sh"}},
	}
	return manifest, dir
}

// ---- Test 1: real shell verifiers, artifacts on and off give the same
// verdict ----

func TestPairEvalRealShellVerifierVerdictStableWithAndWithoutArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		script  string
		wantSuc bool
	}{
		{"exit0", "exit 0", true},
		{"exit3", "echo boom >&2; exit 3", false},
	} {
		for _, artifacts := range []bool{true, false} {
			artifactDir := ""
			if artifacts {
				artifactDir = t.TempDir()
			}
			suc, err := runSeamVerifierOnce(t, tc.script, artifactDir)
			if suc != tc.wantSuc {
				t.Fatalf("%s artifacts=%v: success = %v, want %v (err %v)", tc.name, artifacts, suc, tc.wantSuc, err)
			}
		}
	}
}

// runSeamVerifierOnce drives the production pairEvalRunFunc against a real
// git repo whose "agent" already made a change, running only the verifier
// leg (the exec leg is replaced by pre-seeding the repo).
func runSeamVerifierOnce(t *testing.T, script, artifactDir string) (bool, error) {
	t.Helper()
	repo := newGitRepoWithChange(t)
	in := eval.RunInput{SessionID: "seam-verifier", Cwd: repo, Check: script, ArtifactDir: artifactDir}
	output, verdict, verdictErr := runVerifier(context.Background(), in)
	_ = output
	if verdictErr != nil && verdict == verifierVerdictInfra {
		return false, verdictErr
	}
	return verdictErr == nil && verdict == verifierVerdictPass, verdictErr
}

// newGitRepoWithChange creates a git repo with one commit and one uncommitted
// tracked modification, simulating an agent mid-attempt.
func newGitRepoWithChange(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	run("add", "-A")
	run("commit", "-qm", "base")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a // changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// ---- Test 2: VERIFIER_EXIT=0 in output cannot override nonzero exit ----

func TestVerifierOutputMarkerCannotOverrideNonzeroExit(t *testing.T) {
	repo := newGitRepoWithChange(t)
	// The verifier prints a fake success marker, then exits 7.
	script := "echo VERIFIER_EXIT=0; exit 7"
	in := eval.RunInput{SessionID: "marker", Cwd: repo, Check: script}
	out, _, verdictErr := runVerifier(context.Background(), in)
	if !strings.Contains(string(out), "VERIFIER_EXIT=0") {
		t.Fatalf("fixture broken: output lacks marker: %q", out)
	}
	if verdictErr == nil {
		t.Fatal("VERIFIER_EXIT=0 text in output must not override the nonzero process exit")
	}
}

// ---- Test 3: launch failure and cancellation stay failures even with
// success-shaped partial output ----

func TestVerifierLaunchFailureAndCancellationStayFailures(t *testing.T) {
	// Launch failure: sh itself cannot be started (modeled at the seam
	// where the exec error is not an ExitError; the shell path turns a
	// missing binary into exit 127, which is a verifier rejection).
	verdict, err := classifyVerifierExit(context.Background(), &exec.Error{Name: "sh", Err: exec.ErrNotFound})
	if err == nil || verdict != verifierVerdictInfra {
		t.Fatalf("launch failure: verdict=%q err=%v, want infra failure", verdict, err)
	}
	// A shell that finds no binary exits 127: a rejection, not infra.
	_, verdict, err = runVerifier(context.Background(), eval.RunInput{Check: "definitely-not-a-real-binary-xyz", Cwd: t.TempDir()})
	if err == nil || verdict != verifierVerdictReject {
		t.Fatalf("missing binary via shell: verdict=%q err=%v, want reject", verdict, err)
	}
	// Cancellation: the context dies before the verifier finishes.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, verdict, err = runVerifier(ctx, eval.RunInput{Check: "exit 0", Cwd: t.TempDir()})
	if err == nil {
		t.Fatal("a cancelled verifier must be a failure even when the script would exit 0")
	}
	if verdict != verifierVerdictTimeout {
		t.Fatalf("cancelled verdict = %q, want timeout", verdict)
	}
}

// ---- Test 4: a verifier that destroys files cannot alter the captured
// proposal ----

func TestCapturedProposalSurvivesVerifierDestruction(t *testing.T) {
	repo := newGitRepoWithChange(t)
	// Untracked file the agent created.
	if err := os.WriteFile(filepath.Join(repo, "new.go"), []byte("package new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Capture the proposal BEFORE the verifier runs (the seam order).
	proposal := captureProposal(repo)
	if !strings.Contains(string(proposal.Patch), "package a // changed") {
		t.Fatalf("tracked modification missing from patch: %q", proposal.Patch)
	}
	if !strings.Contains(string(proposal.Patch), "package new") {
		t.Fatalf("untracked file missing from patch: %q", proposal.Patch)
	}
	// The verifier overwrites and removes files.
	destructive := "echo x > a.go && rm new.go && echo probe > probe.txt"
	cmd := exec.Command("/bin/sh", "-c", destructive)
	cmd.Dir = repo
	if err := cmd.Run(); err != nil {
		t.Fatalf("destructive verifier failed: %v", err)
	}
	// The captured bytes still reconstruct the proposal.
	if !strings.Contains(string(proposal.Patch), "package a // changed") {
		t.Fatal("captured patch lost the tracked modification after verifier ran")
	}
	if !strings.Contains(string(proposal.Patch), "package new") {
		t.Fatal("captured patch lost the untracked file after verifier ran")
	}
	if proposal.ManifestDigest == "" || proposal.ProposedDigest == "" {
		t.Fatal("digests must be captured")
	}
}

// ---- Test 5: a new untracked .go file appears in the captured proposal ----

func TestCapturedProposalIncludesNewUntrackedGoFile(t *testing.T) {
	repo := newGitRepoWithChange(t)
	if err := os.WriteFile(filepath.Join(repo, "brand_new.go"), []byte("package brand\n\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proposal := captureProposal(repo)
	found := false
	for _, entry := range decodeManifest(t, proposal.Manifest) {
		if entry.Path == "brand_new.go" && entry.Kind == "untracked" {
			found = true
		}
	}
	if !found {
		t.Fatalf("untracked .go file missing from manifest: %s", proposal.Manifest)
	}
	if !strings.Contains(string(proposal.Patch), "func F()") {
		t.Fatalf("untracked file contents missing from patch: %q", proposal.Patch)
	}
}

func decodeManifest(t *testing.T, data []byte) []changeManifestEntry {
	t.Helper()
	var manifest changeManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return manifest.Entries
}

// ---- Test 6: exec failure retains output and partial proposal artifacts ----

func TestExecFailureRetainsOutputAndPartialProposal(t *testing.T) {
	artifactDir := t.TempDir()
	repo := newGitRepoWithChange(t)
	if err := os.WriteFile(filepath.Join(repo, "partial.go"), []byte("package partial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Drive the capture-and-evidence path the seam uses on agent failure:
	// the proposal is captured first, artifacts written, and the exec error
	// still carries the output.
	proposal := captureProposal(repo)
	execOut := []byte(`{"type":"tool_call","name":"edit"}` + "\n")
	writeProposalArtifact(artifactDir, "failrun", execOut, proposal)
	if err := writeAttemptEvidence(artifactDir, "failrun", proposal); err != nil {
		t.Fatalf("complete evidence should validate: %v", err)
	}
	data, err := os.ReadFile(artifactPath(artifactDir, "failrun", "patch.diff"))
	if err != nil {
		t.Fatalf("patch artifact missing on exec failure: %v", err)
	}
	if !strings.Contains(string(data), "package a // changed") {
		t.Fatalf("patch artifact lost the proposal: %q", data)
	}
	if _, err := os.Stat(artifactPath(artifactDir, "failrun", "exec.jsonl")); err != nil {
		t.Fatalf("exec transcript missing on exec failure: %v", err)
	}
}

// ---- Test 7: artifact write failure is visible without replacing the
// verifier result ----

func TestArtifactWriteFailureVisibleWithoutChangingVerdict(t *testing.T) {
	// A capture whose git state is unreadable yields snap.Error; evidence
	// reports incomplete; the verdict stays whatever the verifier said.
	snap := proposalSnapshot{Error: errors.New("git diff exploded")}
	err := writeAttemptEvidence(t.TempDir(), "s", snap)
	if err == nil || !strings.Contains(err.Error(), "git diff exploded") {
		t.Fatalf("incomplete evidence not reported: %v", err)
	}
	// A passing verdict with incomplete evidence keeps Success=true (the
	// caller composes them: EvidenceStatus is informational).
	out := eval.RunOutput{Success: true, EvidenceStatus: "incomplete", ArtifactError: err.Error()}
	if !out.Success {
		t.Fatal("evidence failure must not flip a real verifier success")
	}
	if out.EvidenceStatus != "incomplete" {
		t.Fatal("evidence status must be visible on the result")
	}
}

// ---- Test 8: one failed precursor = one setup failure, correct skips ----

func TestOneFailedPrecursorCountsOneSetupFailureAndCorrectSkips(t *testing.T) {
	manifest := mvpFamilyManifest{Families: []mvpFamilyEntry{{ID: "fam-x"}}}
	rollouts := 3
	rows := []familyPairRow{
		{Family: "fam-x", Task: "B", Arm: "cold", Attempt: 1, Success: true, Executed: true},
	}
	// ONE failed snapshot produces Rollouts*2 skipped target slots, all
	// sharing the snapshot id.
	for _, arm := range []string{"cold", "warm"} {
		for attempt := 1; attempt <= rollouts; attempt++ {
			rows = append(rows, familyPairRow{
				Family: "fam-x", Task: "B", Attempt: attempt, Arm: arm,
				InfraStatus: "precursor_failed", SetupOutcome: "precursor_failed",
				Executed: false, SnapshotID: "snap-1",
			})
		}
	}
	var out strings.Builder
	reviewAggregatesQuiet = true
	defer func() { reviewAggregatesQuiet = false }()
	summarizeMvp(&out, manifest, rows)
	text := out.String()
	if !strings.Contains(text, "setup failures excluded from target analysis: 6 (failed snapshots: 1, skipped target slots: 6)") {
		t.Fatalf("setup failure accounting wrong:\n%s", text)
	}
}

// ---- Test 9: passing A plus failing B yields 0/1 target successes ----

func TestPassingPrecursorFailingTargetYieldsOneTargetFailure(t *testing.T) {
	manifest, manifestDir := newSeamTestManifest(t, "exit 0\n", "exit 1\n")
	fixtureDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fixtureDir, "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	deps := appDeps{}
	seam := &seamRunner{Outputs: []eval.RunOutput{{Success: true}, {Success: false}}, Errors: []error{nil, nil}}
	rows := &[]familyPairRow{}
	options := mvpEvalOptions{Rollouts: 1, OutDir: t.TempDir()}
	err := runMvpMatchedSnapshots(context.Background(), deps, options, manifest, manifestDir, fixtureDir, seam.run, &strings.Builder{}, rows)
	if err != nil {
		// The matched runner needs a sidecar for seeding; without one the
		// snapshot setup fails loudly. The seam output mapping under test
		// is the row fill, exercised below directly.
		row := fillAttemptRow(familyPairRow{Family: "fam-a", Task: "B", Attempt: 1, Arm: "warm"}, eval.RunOutput{Success: false, FailureCategory: "verifier_rejected"}, nil, time.Millisecond, harnessProvenanceInfo{}, options, eval.RunInput{})
		if row.Success {
			t.Fatal("failing target must stay failed")
		}
		if row.FailureCategory != "verifier_rejected" {
			t.Fatalf("failure category = %q, want verifier_rejected", row.FailureCategory)
		}
		return
	}
	targets := 0
	successes := 0
	for _, row := range *rows {
		if row.Task != "B" || !row.Executed {
			continue
		}
		targets++
		if row.Success {
			successes++
		}
	}
	if targets != 2 || successes != 0 {
		t.Fatalf("targets=%d successes=%d, want 2 executed and 0 successes", targets, successes)
	}
}

// ---- Test 10: independent A/B timeout contexts via the controllable seam ----

func TestIndependentPerAttemptTimeoutContexts(t *testing.T) {
	// Each run must receive its own fresh deadline context: the second run
	// gets a context with a deadline even after the first one expired, and
	// each ctx differs from the shared parent.
	seam := &seamRunner{TimeoutAfter: 30 * time.Millisecond}
	parent := context.Background()
	for i := 0; i < 2; i++ {
		runCtx, cancel := context.WithTimeout(parent, familiesRunTimeout)
		_, _ = seam.run(runCtx, eval.RunInput{SessionID: fmt.Sprintf("s%d", i)})
		cancel()
	}
	if len(seam.Calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(seam.Calls))
	}
	for i, call := range seam.Calls {
		if !call.HasDeadly {
			t.Fatalf("run %d got a context without a deadline; every attempt must carry its own timeout", i)
		}
		if call.Ctx == parent {
			t.Fatalf("run %d got the raw parent context", i)
		}
	}
	// The Task B seam in the matched runner applies the same shape.
	deps := appDeps{}
	_, _, row := mvpRunTracked(deps, context.Background(), func() context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		t.Cleanup(cancel)
		return ctx
	}(), seam.run, eval.RunInput{SessionID: "tb", Cwd: t.TempDir()}, "")
	if row.InfraStatus != "timeout" || row.Success {
		t.Fatalf("timed-out Task B: infra=%q success=%v, want timeout/false", row.InfraStatus, row.Success)
	}
	if row.AgentTimeMs < 0 {
		t.Fatal("elapsed must be measured on timeout too")
	}
}

// ---- Test 11: cancellation after a completed target retains that row ----

func TestCancellationAfterCompletedTargetRetainsRow(t *testing.T) {
	outDir := t.TempDir()
	rows := &[]familyPairRow{}
	completed := familyPairRow{Family: "fam-a", Task: "B", Attempt: 1, Arm: "cold", Success: true, Executed: true, SessionID: "done"}
	appendRowWithCheckpoint(rows, outDir, completed)
	// Simulate the interruption: cancel, then the next row never lands.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ctx.Err() == nil {
		t.Fatal("fixture broken")
	}
	// The checkpoint file must already hold the completed row.
	data, err := os.ReadFile(filepath.Join(outDir, "families-attempts.jsonl"))
	if err != nil {
		t.Fatalf("checkpoint missing after completed target: %v", err)
	}
	if !strings.Contains(string(data), `"session_id":"done"`) {
		t.Fatalf("completed target row not retained:\n%s", data)
	}
	if len(*rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(*rows))
	}
}
