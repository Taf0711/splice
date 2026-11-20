package splice

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/worktrees"
)

// testBaseline answers what a test command reported at the run's pre-run
// state. The test runner asks only about failing tests, so a failure that
// was already present is told apart from one the run caused. Each distinct
// command runs at most once per run, in a temporary checkout of the snapshot.
// defaultBaselineStage runs the baseline command with the test runner stage.
func defaultBaselineStage(ctx context.Context, input schemas.HarnessStageInput, opts stages.StageOptions) (schemas.HarnessStageOutput, error) {
	return stages.TestRunner{}.Run(ctx, input, nil, opts)
}

// runBaselineStage runs one baseline command. A test may replace it to see
// the options the command runs with.
var runBaselineStage = defaultBaselineStage

type testBaseline struct {
	root   string
	commit string
	// pinned is the snapshot ref this run created. It is "" when the
	// worktree matched HEAD, because then no ref was made.
	pinned string
	cache  map[string]baselineAnswer
	// execute runs one command in the baseline. It is run by default and a
	// test can replace it.
	execute func(ctx context.Context, options stages.StageOptions, argv []string, dir string) (schemas.TestRunResults, bool)
}

// staleCheckoutAge is how old a baseline checkout must be before a new capture
// removes it. A live run's checkout is far younger.
const staleCheckoutAge = time.Hour

type baselineAnswer struct {
	results schemas.TestRunResults
	ok      bool
}

// captureTestBaseline snapshots the workspace as it is before the first
// pass. When the snapshot fails, the baseline never answers and note says
// why. The run still goes on, and every failure stays new.
func captureTestBaseline(ctx context.Context, root string, note func(string)) *testBaseline {
	b := &testBaseline{root: root, cache: map[string]baselineAnswer{}}
	b.execute = b.run
	head := headRevision(ctx, root)
	commit, err := worktrees.SnapshotCommit(ctx, root, "splice pre-run baseline")
	if err != nil {
		note(fmt.Sprintf("[tests] pre-run state unavailable, so failures are not compared with it: %v", err))
		return b
	}
	b.commit = commit
	// A run that crashed before its cleanup left a checkout behind. Sweep the
	// stale ones now. A failed sweep does not stop the run.
	_, _ = worktrees.PruneStaleCheckouts(ctx, root, staleCheckoutAge)
	if commit != head {
		b.pinned = commit
	}
	return b
}

// Release deletes the snapshot ref that this run created. A cognition
// snapshot has its own commit and ref, so it is never deleted here.
func (b *testBaseline) Release(ctx context.Context) {
	if b == nil || b.pinned == "" {
		return
	}
	// The run may be cancelled, so the cleanup uses its own context.
	_ = worktrees.ReleaseSnapshot(context.Background(), b.root, b.pinned)
	b.pinned = ""
}

// Results returns the per-test results of argv run in dir at the pre-run
// state. ok is false when no baseline is available.
func (b *testBaseline) Results(ctx context.Context, options stages.StageOptions, argv []string, dir string) (schemas.TestRunResults, bool) {
	if b == nil || b.commit == "" {
		return schemas.TestRunResults{}, false
	}
	key := dir + "\x00" + strings.Join(argv, "\x00")
	if answer, ok := b.cache[key]; ok {
		return answer.results, answer.ok
	}
	results, ok := b.execute(ctx, options, argv, dir)
	b.cache[key] = baselineAnswer{results: results, ok: ok}
	return results, ok
}

// run checks the snapshot out in a temporary worktree, runs the command there
// with the same test runner, and removes the checkout before it returns.
func (b *testBaseline) run(ctx context.Context, options stages.StageOptions, argv []string, dir string) (schemas.TestRunResults, bool) {
	rel, err := filepath.Rel(b.root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return schemas.TestRunResults{}, false
	}
	checkout, cleanup, err := worktrees.CheckoutAt(ctx, b.root, b.commit)
	if err != nil {
		return schemas.TestRunResults{}, false
	}
	defer cleanup()

	baseDir := filepath.Join(checkout, rel)
	stageOpts := options
	stageOpts.TestBaseline = nil
	stageOpts.WorkDir = baseDir
	stageOpts.CommandWorkDir = ""
	stageOpts.Command = baselineArgv(argv, baseDir)
	stageOpts.RunTool = nil
	stageOpts.RecordCommand = nil
	if options.Sandbox != nil {
		// The same policy as the run, bound to the checkout. The checkout is
		// outside the run's workspace, so the run's engine would refuse it.
		stageOpts.Sandbox = options.Sandbox.StageEngine(checkout, "")
	}
	out, err := runBaselineStage(ctx, schemas.HarnessStageInput{}, stageOpts)
	if err != nil {
		return schemas.TestRunResults{}, false
	}
	results, ok := out.Data["test_results"].(schemas.TestRunResults)
	if !ok || results.Evidence != schemas.TestEvidencePerTest {
		return schemas.TestRunResults{}, false
	}
	return results, true
}

// baselineArgv drops the path arguments that dir does not have. A test file
// that this run added has no baseline failures, so its path must not reach
// the baseline command. The program name, flags, patterns, and values stay.
func baselineArgv(argv []string, dir string) []string {
	out := make([]string, 0, len(argv))
	for i, arg := range argv {
		if i > 0 && isPathArgument(arg) {
			path := arg
			if cut := strings.Index(path, "::"); cut >= 0 {
				path = path[:cut]
			}
			if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
				continue
			}
		}
		out = append(out, arg)
	}
	return out
}

// isPathArgument reports whether arg names a file or directory. Flags and
// patterns are not paths.
func isPathArgument(arg string) bool {
	if arg == "" || strings.HasPrefix(arg, "-") || strings.ContainsAny(arg, "*?[") || strings.Contains(arg, "...") {
		return false
	}
	if strings.Contains(arg, "/") {
		return true
	}
	for _, ext := range []string{".py", ".go", ".js", ".jsx", ".ts", ".tsx"} {
		if strings.HasSuffix(arg, ext) {
			return true
		}
	}
	return false
}

// hasTestRunnerStage reports whether the plan runs a test runner.
func hasTestRunnerStage(plan schemas.ExecutionPlan) bool {
	for _, stage := range plan.Stages {
		if stageRole(stage) == "test_runner" {
			return true
		}
	}
	return false
}
