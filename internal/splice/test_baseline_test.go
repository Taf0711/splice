package splice

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/worktrees"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// baselineGit runs git in dir for the baseline tests and fails on error.
func baselineGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// baselineRepo writes files into a new Go repo and commits them as the base.
func baselineRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/fixture\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		writeBaselineFile(t, dir, name, content)
	}
	baselineGit(t, dir, "init", "-q")
	baselineGit(t, dir, "add", "-A")
	baselineGit(t, dir, "commit", "-qm", "base")
	return dir
}

func writeBaselineFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func requireGoSandbox(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go is not available: %v", err)
	}
	if backend := sandbox.SelectBackend(sandbox.BackendOptions{}); !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
}

// Test fixtures for the pipeline. The base has a failing test that the run
// did not cause, and the writer adds code and tests.
const (
	fixtureAdd     = "package fixture\n\nfunc Add(a, b int) int { return a + b }\n"
	fixtureAddTest = "package fixture\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 1) != 2 {\n\t\tt.Fatal(\"Add\")\n\t}\n}\n"
	fixtureBroken  = "package fixture\n\nimport \"testing\"\n\nfunc TestAlreadyBroken(t *testing.T) {\n\tt.Fatal(\"broken at base\")\n}\n"
)

// runBaselinePipeline runs code_writer and the real test runner through the
// pass loop. The writer returns the files for each call. It returns the
// result and the number of writer calls.
func runBaselinePipeline(t *testing.T, workDir string, writer func(call int) map[string]string) (schemas.PipelineResult, int) {
	t.Helper()
	calls := 0
	writerStage := stageFunc(func(_ context.Context, _ schemas.HarnessStageInput, _ zeroruntime.Provider, _ stages.StageOptions) (schemas.HarnessStageOutput, error) {
		calls++
		var files []schemas.FileChange
		for name, content := range writer(calls) {
			writeBaselineFile(t, workDir, name, content)
			files = append(files, schemas.FileChange{Path: name, Content: content, ChangeType: "modify"})
		}
		return schemas.HarnessStageOutput{
			Summary: fmt.Sprintf("wrote %d file(s)", len(files)), Confidence: 1,
			Data: map[string]any{"code_writer_output": schemas.CodeWriterOutput{Files: files, Language: "go", Intent: "fix Add", Confidence: 1}},
		}, nil
	})
	registry := stageRegistry{"code_writer": writerStage, "test_runner": stages.TestRunner{}}
	plan := schemas.ExecutionPlan{
		Tier: schemas.TierStandard, RequestIntent: "make Add correct and add a test",
		Stages: []schemas.ExecutionStage{
			{Name: "code_writer", Type: "code_writer"},
			{Name: "test_runner", Type: "test_runner"},
		},
		TokenBudget: schemas.TokenBudget{TotalOutputBudget: 1_000_000, OverflowPolicy: "abort"},
	}
	options := PipelineConfigFromAgentOptions(agent.Options{Cwd: workDir})
	options.StageSandbox = procrun.NewStageEngine(workDir)
	options.VerifyCommand = []string{"go", "test", "./..."}
	options.MaxWall = 5 * time.Minute
	options.testBaseline = captureTestBaseline(context.Background(), workDir, func(string) {})
	result, err := runIterationLoop(context.Background(), "run-baseline", plan, registry, runFakeProvider{}, options, workDir, nil, nil, nil, nil, nil, newRequestLedger())
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	return result, calls
}

// testsCheck returns the tests check of a result, or fails the test.
func testsCheck(t *testing.T, result schemas.PipelineResult) schemas.VerificationCheck {
	t.Helper()
	for _, check := range result.VerificationChecks {
		if check.Kind == schemas.CheckKindTests {
			return check
		}
	}
	t.Fatalf("no tests check in %+v", result.VerificationChecks)
	return schemas.VerificationCheck{}
}

// A failure the run did not cause does not block verification. The run adds
// a passing test, so it verifies with no repair, and the receipt names the
// failure that was already there.
func TestPipelineVerifiesWhenOnlyPreexistingTestFails(t *testing.T) {
	requireGoSandbox(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	workDir := baselineRepo(t, map[string]string{
		"add.go":          fixtureAdd,
		"add_test.go":     fixtureAddTest,
		"broken_test.go":  fixtureBroken,
		"notes/notes.txt": "x\n",
	})
	result, calls := runBaselinePipeline(t, workDir, func(call int) map[string]string {
		if call == 1 {
			return map[string]string{"add.go": fixtureAdd}
		}
		return nil
	})
	if !result.Verified {
		t.Fatalf("run must verify: outcome %q reason %q checks %+v", result.VerificationOutcome, result.UnverifiedReason, result.VerificationChecks)
	}
	if calls != 1 {
		t.Fatalf("writer calls = %d, want 1 (no repair for a pre-existing failure)", calls)
	}
	check := testsCheck(t, result)
	if len(check.AlreadyFailing) != 1 || check.AlreadyFailing[0] != "example.com/fixture.TestAlreadyBroken" {
		t.Fatalf("already failing = %v, want the base failure by name", check.AlreadyFailing)
	}
	assertNoBaselineLeftovers(t, workDir)
}

// A passing test that the run breaks is a new failure. Repair runs and the
// run does not verify.
func TestPipelineBreakingPassingTestStaysNew(t *testing.T) {
	requireGoSandbox(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	workDir := baselineRepo(t, map[string]string{"add.go": fixtureAdd, "add_test.go": fixtureAddTest})
	result, calls := runBaselinePipeline(t, workDir, func(call int) map[string]string {
		return map[string]string{"add.go": "package fixture\n\nfunc Add(a, b int) int { return a - b }\n"}
	})
	if result.Verified {
		t.Fatal("a broken passing test must not verify")
	}
	if calls < 2 {
		t.Fatalf("writer calls = %d, want repair attempts after the new failure", calls)
	}
	if summary := testRunnerSummary(t, result); strings.Contains(summary, "already failing") {
		t.Fatalf("a broken passing test must not be marked as already failing: %q", summary)
	}
}

// A new failing test file is a new failure. Its path is not in the baseline
// command, and its test is not in the baseline checkout.
func TestPipelineNewFailingTestFileStaysNew(t *testing.T) {
	requireGoSandbox(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	workDir := baselineRepo(t, map[string]string{"add.go": fixtureAdd, "add_test.go": fixtureAddTest})
	result, calls := runBaselinePipeline(t, workDir, func(call int) map[string]string {
		return map[string]string{"new_test.go": "package fixture\n\nimport \"testing\"\n\nfunc TestNew(t *testing.T) {\n\tt.Fatal(\"new\")\n}\n"}
	})
	if result.Verified {
		t.Fatal("a new failing test must not verify")
	}
	if calls < 2 {
		t.Fatalf("writer calls = %d, want repair attempts for the new failure", calls)
	}
	if summary := testRunnerSummary(t, result); strings.Contains(summary, "already failing") {
		t.Fatalf("the new test must not be marked as already failing: %q", summary)
	}
}

// testRunnerSummary returns the summary of the last test runner record. A run
// that does not verify has no verification checks, so the record is the place
// the failure is reported.
func testRunnerSummary(t *testing.T, result schemas.PipelineResult) string {
	t.Helper()
	for i := len(result.Stages) - 1; i >= 0; i-- {
		if result.Stages[i].Name == "test_runner" && result.Stages[i].OutputSummary != nil {
			return *result.Stages[i].OutputSummary
		}
	}
	t.Fatal("no test_runner record with a summary")
	return ""
}

// The baseline is the state before the run. A user change that was already
// uncommitted, and breaks a test, is part of that state, not the run.
func TestPipelineBaselineIsPreRunStateNotHead(t *testing.T) {
	requireGoSandbox(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	workDir := baselineRepo(t, map[string]string{"add.go": fixtureAdd, "add_test.go": fixtureAddTest})
	// The user breaks Add before the run. HEAD still has the working code.
	writeBaselineFile(t, workDir, "add.go", "package fixture\n\nfunc Add(a, b int) int { return a - b }\n")
	result, _ := runBaselinePipeline(t, workDir, func(call int) map[string]string { return nil })
	check := testsCheck(t, result)
	if len(check.AlreadyFailing) != 1 || check.AlreadyFailing[0] != "example.com/fixture.TestAdd" {
		t.Fatalf("already failing = %v, want TestAdd from the pre-run state", check.AlreadyFailing)
	}
}

// assertNoBaselineLeftovers fails when a baseline checkout is still in the
// managed home of the repository at dir.
func assertNoBaselineLeftovers(t *testing.T, dir string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(worktrees.CheckoutHome(resolved))
	if len(entries) > 0 {
		t.Fatalf("baseline checkout left behind: %d entries", len(entries))
	}
}

// The baseline runs at most once per distinct command in a run.
func TestTestBaselineRunsEachCommandOnce(t *testing.T) {
	calls := map[string]int{}
	b := &testBaseline{root: t.TempDir(), commit: "abc", cache: map[string]baselineAnswer{}}
	b.execute = func(_ context.Context, _ stages.StageOptions, argv []string, dir string) (schemas.TestRunResults, bool) {
		calls[strings.Join(argv, " ")+"@"+dir]++
		return schemas.TestRunResults{Evidence: schemas.TestEvidencePerTest}, true
	}
	a := []string{"go", "test", "./..."}
	other := []string{"go", "test", "./pkg"}
	for i := 0; i < 3; i++ {
		if _, ok := b.Results(context.Background(), stages.StageOptions{}, a, b.root); !ok {
			t.Fatal("baseline must answer")
		}
	}
	if _, ok := b.Results(context.Background(), stages.StageOptions{}, other, b.root); !ok {
		t.Fatal("baseline must answer for a second command")
	}
	if len(calls) != 2 || calls["go test ./...@"+b.root] != 1 || calls["go test ./pkg@"+b.root] != 1 {
		t.Fatalf("calls = %v, want one run per distinct command", calls)
	}
}

// Without a snapshot the baseline never answers, so no failure is marked.
func TestTestBaselineUnavailableWithoutSnapshot(t *testing.T) {
	ran := false
	b := &testBaseline{root: t.TempDir(), cache: map[string]baselineAnswer{}}
	b.execute = func(context.Context, stages.StageOptions, []string, string) (schemas.TestRunResults, bool) {
		ran = true
		return schemas.TestRunResults{}, true
	}
	if _, ok := b.Results(context.Background(), stages.StageOptions{}, []string{"go", "test"}, b.root); ok || ran {
		t.Fatalf("baseline without a commit must not answer or run (ok=%v ran=%v)", ok, ran)
	}
}

// A cancelled run removes its baseline checkout and worktree registration.
func TestTestBaselineCancelRemovesCheckout(t *testing.T) {
	requireGoSandbox(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dir := baselineRepo(t, map[string]string{
		"slow_test.go": "package fixture\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestSlow(t *testing.T) {\n\ttime.Sleep(60 * time.Second)\n}\n",
		"add.go":       fixtureAdd,
	})
	b := captureTestBaseline(context.Background(), dir, func(string) {})
	if b.commit == "" {
		t.Fatal("baseline snapshot must succeed in a clean repo")
	}
	before := baselineGit(t, dir, "worktree", "list", "--porcelain")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		_, ok := b.Results(ctx, stages.StageOptions{Sandbox: procrun.NewStageEngine(dir), TimeoutSeconds: 120}, []string{"go", "test", "./..."}, dir)
		done <- ok
	}()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	home := worktrees.CheckoutHome(resolved)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if entries, _ := os.ReadDir(home); len(entries) > 0 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("baseline checkout never appeared")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("a cancelled baseline must not answer")
		}
	case <-time.After(60 * time.Second):
		t.Fatal("cancelled baseline did not return")
	}
	if entries, _ := os.ReadDir(home); len(entries) > 0 {
		t.Fatalf("baseline checkout left behind after cancel: %d entries", len(entries))
	}
	if after := baselineGit(t, dir, "worktree", "list", "--porcelain"); after != before {
		t.Fatalf("worktree list changed after cancel:\n%s", after)
	}
}

// Only path arguments that the baseline checkout lacks are dropped. Flags,
// patterns, the program name, and node ids of existing files stay.
func TestBaselineArgvDropsMissingPathsOnly(t *testing.T) {
	dir := t.TempDir()
	writeBaselineFile(t, dir, "tests/test_old.py", "def test_a():\n    pass\n")
	argv := []string{"./bin/missing", "-k", "fast or slow", "tests/test_old.py", "tests/test_new.py", "tests/test_old.py::test_a", "tests/test_new.py::test_b", "other_new.py", "./..."}
	got := baselineArgv(argv, dir)
	want := []string{"./bin/missing", "-k", "fast or slow", "tests/test_old.py", "tests/test_old.py::test_a", "./..."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("baselineArgv = %q, want %q", got, want)
	}
}

// IterationState counts the new failures in its failing counters and the
// pre-existing ones in TestsAlreadyFailing.
func TestIterationStateCountsOnlyNewFailures(t *testing.T) {
	results := schemas.TestRunResults{Command: []string{"go", "test"}, Evidence: schemas.TestEvidencePerTest, Tests: []schemas.TestCaseResult{
		{Name: "example.com/p.TestOld", Package: "example.com/p", Status: "failed", AlreadyFailing: true},
		{Name: "example.com/p.TestNew", Package: "example.com/p", Status: "failed"},
		{Name: "example.com/p.TestOk", Package: "example.com/p", Status: "passed"},
	}}
	outputs := []schemas.HarnessStageOutput{{Stage: "test_runner", Data: map[string]any{"test_results": results}}}
	ts := 1.0
	state, err := ComputeIterationState(1, outputs, nil, schemas.ChangeSummary{}, &ts)
	if err != nil {
		t.Fatalf("ComputeIterationState: %v", err)
	}
	if state.TestsFailing != 1 || state.TestsPassing != 1 || state.TestsAlreadyFailing != 1 {
		t.Fatalf("failing=%d passing=%d already=%d, want 1, 1, 1", state.TestsFailing, state.TestsPassing, state.TestsAlreadyFailing)
	}
}

// A run whose only failures predate it, with no passing test, keeps its
// pre-existing count in the check detail.
func TestTestsCheckKeepsAlreadyFailingWhenNothingPassed(t *testing.T) {
	results := schemas.TestRunResults{Command: []string{"go", "test"}, ExitCode: 1, Evidence: schemas.TestEvidencePerTest, Tests: []schemas.TestCaseResult{
		{Name: "example.com/p.TestOld", Package: "example.com/p", Status: "failed", AlreadyFailing: true},
	}}
	outcome, detail := testsCheckOutcome(results)
	if outcome != schemas.CheckNoTests || !strings.Contains(detail, "1 already failing before this run") {
		t.Fatalf("outcome = %q, detail = %q; want no tests with the already-failing count", outcome, detail)
	}
}

// The work record counts the tests that predate the run as executed, and it
// names them in the observed result. It does not count them as failed.
func TestCaptureVerificationCountsAlreadyFailing(t *testing.T) {
	state := schemas.IterationState{TestsPassing: 3, TestsFailing: 1, TestsAlreadyFailing: 2}
	result := schemas.PipelineResult{
		Stages:     []schemas.StageRecord{{Name: "test_runner", Status: schemas.StageCompleted}},
		FinalState: &state,
	}
	ver := captureVerificationFromResult(result, "test_runner")
	if ver.TestsExecuted != 6 || ver.TestsFailed != 1 {
		t.Fatalf("executed = %d, failed = %d; want 6 and 1", ver.TestsExecuted, ver.TestsFailed)
	}
	if !strings.Contains(ver.ObservedResult, "2 already failing before the run") {
		t.Fatalf("observed result = %q, want the already-failing count", ver.ObservedResult)
	}
}

// refExists reports whether a snapshot ref for sha is pinned in dir.
func refExists(t *testing.T, dir, sha string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", "refs/splice/snapshots/"+sha)
	return cmd.Run() == nil
}

// The baseline pins a snapshot ref only when it creates a new commit. Release
// deletes that ref and leaves a cognition snapshot in the same repo alone.
func TestBaselineSnapshotRefLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := baselineRepo(t, map[string]string{"add.go": fixtureAdd, "add_test.go": fixtureAddTest})

	clean := captureTestBaseline(ctx, dir, func(string) {})
	if clean.pinned != "" {
		t.Fatalf("clean worktree pinned %q, want no ref", clean.pinned)
	}
	clean.Release(ctx)

	writeBaselineFile(t, dir, "add.go", "package fixture\n\nfunc Add(a, b int) int { return a - b }\n")
	b := captureTestBaseline(ctx, dir, func(string) {})
	if b.pinned == "" || !refExists(t, dir, b.pinned) {
		t.Fatalf("dirty worktree must pin its baseline snapshot, pinned=%q", b.pinned)
	}
	cognition, err := worktrees.SnapshotCommit(ctx, dir, "splice cognition capture snapshot")
	if err != nil || cognition == b.pinned {
		t.Fatalf("cognition snapshot = %q, %v; want a different commit", cognition, err)
	}

	b.Release(ctx)
	if refExists(t, dir, b.pinned) {
		t.Fatal("Release must delete the baseline ref this run created")
	}
	if !refExists(t, dir, cognition) {
		t.Fatal("Release must not delete the cognition snapshot ref")
	}
}

// Capturing a baseline removes the checkouts that a crashed run left behind.
func TestCaptureBaselineSweepsCrashedCheckout(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	dir := baselineRepo(t, map[string]string{"add.go": fixtureAdd})
	leaked, _, err := worktrees.CheckoutAt(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatalf("CheckoutAt: %v", err)
	}
	// Git reports the resolved top-level path, so the managed home is keyed by it.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	home := worktrees.CheckoutHome(resolved)
	old := time.Now().Add(-2 * time.Hour)
	entries, _ := os.ReadDir(home)
	for _, entry := range entries {
		if err := os.Chtimes(filepath.Join(home, entry.Name()), old, old); err != nil {
			t.Fatal(err)
		}
	}
	captureTestBaseline(context.Background(), dir, func(string) {})
	if _, err := os.Stat(leaked); !os.IsNotExist(err) {
		t.Fatalf("crashed checkout survived the baseline capture: %v", err)
	}
}

// The repair text lists only the new failures. The raw output is withheld
// when a failure predates the run, and sent when none does.
func TestRepairDiagnosticsNewFailuresOnly(t *testing.T) {
	mixed := schemas.TestRunResults{Stdout: "raw output with TestOld and TestNew", Tests: []schemas.TestCaseResult{
		{Name: "example.com/p.TestOld", Package: "example.com/p", Status: "failed", Message: "old boom", AlreadyFailing: true},
		{Name: "example.com/p.TestNew", Package: "example.com/p", Status: "failed", Message: "new boom"},
	}}
	got := repairDiagnostics(mixed)
	if !strings.Contains(got, "example.com/p.TestNew: new boom") {
		t.Fatalf("diagnostics = %q, want the new failure", got)
	}
	if strings.Contains(got, "TestOld") || strings.Contains(got, "raw output") {
		t.Fatalf("diagnostics = %q, must not carry the pre-existing failure or the raw output", got)
	}
	clean := schemas.TestRunResults{Stdout: "raw output", Tests: []schemas.TestCaseResult{
		{Name: "example.com/p.TestNew", Package: "example.com/p", Status: "failed", Message: "new boom"},
	}}
	if got := repairDiagnostics(clean); !strings.Contains(got, "raw output") {
		t.Fatalf("with no pre-existing failure the raw output must be sent: %q", got)
	}
}

// Repair evidence and the failure fingerprint use new failures only. A run
// with an extra pre-existing failure has the same fingerprint as one without.
func TestRepairEvidenceAndFingerprintIgnoreAlreadyFailing(t *testing.T) {
	base := schemas.TestCaseResult{Name: "example.com/p.TestOld", Package: "example.com/p", Status: "failed", Message: "old", AlreadyFailing: true}
	newOnly := schemas.TestRunResults{Command: []string{"go", "test", "./..."}, ExitCode: 1, Evidence: schemas.TestEvidencePerTest, Tests: []schemas.TestCaseResult{
		{Name: "example.com/p.TestNew", Package: "example.com/p", Status: "failed", Message: "new"},
	}}
	withOld := newOnly
	withOld.Tests = append([]schemas.TestCaseResult{base}, newOnly.Tests...)

	evidence, names := extractFailingTests(withOld)
	if len(names) != 1 || names[0] != "TestNew" || len(evidence) != 1 {
		t.Fatalf("extractFailingTests = %v, %v; want only TestNew", evidence, names)
	}
	if FingerprintFromTestResults(withOld).Hash() != FingerprintFromTestResults(newOnly).Hash() {
		t.Fatal("the fingerprint must not change with a pre-existing failure")
	}
}

// The baseline command runs in the checkout, under a stage engine rooted at
// the checkout. The run's own workspace is outside that engine.
func TestBaselineRunsInCheckoutUnderCheckoutEngine(t *testing.T) {
	requireGoSandbox(t)
	t.Setenv("TMPDIR", t.TempDir())
	dir := baselineRepo(t, map[string]string{"add.go": fixtureAdd, "add_test.go": fixtureAddTest})
	b := captureTestBaseline(context.Background(), dir, func(string) {})
	var seen stages.StageOptions
	runBaselineStage = func(ctx context.Context, input schemas.HarnessStageInput, opts stages.StageOptions) (schemas.HarnessStageOutput, error) {
		seen = opts
		return stages.TestRunner{}.Run(ctx, input, nil, opts)
	}
	defer func() { runBaselineStage = defaultBaselineStage }()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Results(context.Background(), stages.StageOptions{Sandbox: procrun.NewStageEngine(dir), TimeoutSeconds: 120}, []string{"go", "test", "./..."}, dir); !ok {
		t.Fatal("baseline must answer in a clean repo")
	}
	if seen.Sandbox == nil {
		t.Fatal("baseline must run under a stage engine")
	}
	root := seen.Sandbox.WorkspaceRoot()
	if root == resolved || strings.HasPrefix(seen.WorkDir, resolved) {
		t.Fatalf("baseline engine root %q and workdir %q must be the checkout, not the run workspace %q", root, seen.WorkDir, resolved)
	}
	if !strings.HasPrefix(seen.WorkDir, root) {
		t.Fatalf("workdir %q must sit under the engine root %q", seen.WorkDir, root)
	}
}

// A run captures the pre-run baseline. A workspace that is not a git repo
// cannot be snapshotted, so the run says so in its progress.
func TestRunCapturesPreRunBaseline(t *testing.T) {
	requireGoSandbox(t)
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var reasoning []string
	_, _ = Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		PermissionMode: agent.PermissionModeAuto,
		OnReasoning:    func(s string) { reasoning = append(reasoning, s) },
	}, nil, nil)
	if joined := strings.Join(reasoning, ""); !strings.Contains(joined, "pre-run state unavailable") {
		t.Fatalf("the run must capture the pre-run baseline and report it, progress was:\n%s", joined)
	}
}
