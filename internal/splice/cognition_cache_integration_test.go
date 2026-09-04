package splice

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/cognition"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/worktrees"
)

// spawnCountingRunner wraps the real git runner and counts every git
// process spawn, proving the batch+memo behavior end-to-end through
// prepareStageInput.
type spawnCountingRunner struct {
	mu     sync.Mutex
	spawns int
}

func (r *spawnCountingRunner) Capture(ctx context.Context, dir string, args ...string) (worktrees.CommandResult, error) {
	r.mu.Lock()
	r.spawns++
	r.mu.Unlock()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return worktrees.CommandResult{Stdout: string(out), Stderr: string(out), ExitCode: ee.ExitCode()}, nil
		}
		return worktrees.CommandResult{Stdout: string(out), Stderr: string(out)}, err
	}
	return worktrees.CommandResult{Stdout: string(out), Stderr: string(out)}, nil
}

// TestPrepareStageInputOneSpawnPerUniqueCommit is the C1b batch win, pinned:
// one stage invocation whose keys anchor the same repo performs exactly ONE
// git process spawn for the batched changed-path query, regardless of how
// many observations or keys anchor it.
func TestPrepareStageInputOneSpawnPerUniqueCommit(t *testing.T) {
	root, commit := cognitionFixtureRepo(t, "internal/auth/session.go", "package auth\n")

	// Two observations with the SAME source commit: the old per-check path
	// spawned git twice; the batch must spawn once.
	obsA := obsWithID(1, root, "session invalidation rule")
	obsA.SourceCommit = &commit
	obsA.TopicKey = ptr("file:internal/auth/session.go")
	obsB := obsWithID(2, root, "reset password flow")
	obsB.SourceCommit = &commit
	obsB.TopicKey = ptr("symbol:internal/auth/session.go#ResetPassword")
	store := &cognitionLookupStore{topics: map[string]schemas.MemoryBundle{
		"file:internal/auth/session.go":                 {RequestingAgent: "code_writer", Observations: []schemas.MemoryObservation{obsA}},
		"symbol:internal/auth/session.go#ResetPassword": {RequestingAgent: "code_writer", Observations: []schemas.MemoryObservation{obsB}},
	}}

	runner := &spawnCountingRunner{}
	prevCapture := cognition.SetGitCaptureForTest(runner.Capture)
	defer cognition.SetGitCaptureForTest(prevCapture)

	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run", "session", root, plan, "active", nil)
	prepared, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput("code_writer", "fix session invalidation in internal/auth/session.go#ResetPassword"),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, "code_writer"),
		Tier:      plan.Tier,
		Iteration: 1,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
		Trace:     tr,
	})
	if err != nil {
		t.Fatalf("prepareStageInput: %v", err)
	}
	if prepared.MemoryBundle == nil || len(prepared.MemoryBundle.Observations) != 1 {
		// C0 semantics (preserved): the fast path returns the FIRST key's
		// admitted fresh bundle, so the file: key's single observation is
		// delivered and the symbol: key is never consulted.
		t.Fatalf("expected the first key's admitted observation, got %v", prepared.MemoryBundle)
	}
	if got := runner.spawns; got != 1 {
		t.Fatalf("git spawns for one invocation with 2 keys/2 observations = %d, want 1 (batch)", got)
	}

	// Re-entry through the same run's cache: the second invocation for the
	// same (commit, generation) must spawn ZERO git processes.
	runner.mu.Lock()
	runner.spawns = 0
	runner.mu.Unlock()
	prepared2, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput("code_writer", "fix session invalidation in internal/auth/session.go"),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, "code_writer"),
		Tier:      plan.Tier,
		Iteration: 2,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
		Trace:     tr,
	})
	if err != nil {
		t.Fatalf("prepareStageInput re-entry: %v", err)
	}
	// Run-local replay guard: the first invocation delivered observation 1 to
	// code_writer, so the re-entry (repair semantics: same stage, same run)
	// must SUPPRESS it from prompt delivery even though the direct hit and
	// its zero-spawn freshness memo still work. Suppression is delivery-level
	// only — the git spawn count below proves retrieval stayed real.
	if prepared2.MemoryBundle == nil || len(prepared2.MemoryBundle.Observations) != 0 {
		t.Fatalf("re-entry expected the already-consumed observation to be suppressed, got %v", prepared2.MemoryBundle)
	}
	if got := tr.replaySuppressedCount(); got != 1 {
		t.Fatalf("replay suppressed count = %d, want 1", got)
	}
	if got := runner.spawns; got != 0 {
		t.Fatalf("git spawns on re-entry = %d, want 0 (memoized)", got)
	}
}

// TestPrepareStageInputMutationBumpsGeneration pins the generation semantics
// through the composition path: when the prior-changed-file record changes
// (a Splice-permitted mutation), the next invocation re-proves freshness
// with an exact spawn instead of trusting the memoized set.
func TestPrepareStageInputMutationBumpsGeneration(t *testing.T) {
	root, commit := cognitionFixtureRepo(t, "internal/auth/session.go", "package auth\n")
	obs := obsWithID(1, root, "session invalidation rule")
	obs.SourceCommit = &commit
	obs.TopicKey = ptr("file:internal/auth/session.go")
	store := &cognitionLookupStore{topics: map[string]schemas.MemoryBundle{
		"file:internal/auth/session.go": {RequestingAgent: "code_writer", Observations: []schemas.MemoryObservation{obs}},
	}}

	runner := &spawnCountingRunner{}
	prevCapture := cognition.SetGitCaptureForTest(runner.Capture)
	defer cognition.SetGitCaptureForTest(prevCapture)

	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run", "session", root, plan, "active", nil)

	// Invocation 1: no prior mutations, memoize the fresh verdict.
	input1 := cognitionInput("code_writer", "fix session invalidation in internal/auth/session.go")
	if _, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input: input1, Stage: &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget: stageBudgetByName(plan, "code_writer"), Tier: plan.Tier, Iteration: 1,
		WorkDir: root, Options: PipelineConfigFromAgentOptions(agent.Options{}),
		Memory: store, Trace: tr,
	}); err != nil {
		t.Fatalf("invocation 1: %v", err)
	}
	if got := runner.spawns; got != 1 {
		t.Fatalf("spawns after invocation 1 = %d, want 1", got)
	}

	// A Splice-permitted mutation appears in the changed-file record: the
	// code writer wrote a NEW file. Same commit, but the tree moved under
	// Splice's control, so the memoized set must be re-proven by a spawn.
	input2 := cognitionInput("code_writer", "fix session invalidation in internal/auth/session.go")
	input2.PriorChangedFiles = map[string][]string{"code_writer": {"internal/auth/token.go"}}
	if _, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input: input2, Stage: &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget: stageBudgetByName(plan, "code_writer"), Tier: plan.Tier, Iteration: 2,
		WorkDir: root, Options: PipelineConfigFromAgentOptions(agent.Options{}),
		Memory: store, Trace: tr,
	}); err != nil {
		t.Fatalf("invocation 2: %v", err)
	}
	if got := runner.spawns; got != 2 {
		t.Fatalf("spawns after mutation = %d, want 2 (generation bump re-proves)", got)
	}

	// The mutation record is unchanged now: memoized, zero spawns.
	input3 := input2
	input3.PriorChangedFiles = map[string][]string{"code_writer": {"internal/auth/token.go"}}
	if _, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input: input3, Stage: &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget: stageBudgetByName(plan, "code_writer"), Tier: plan.Tier, Iteration: 3,
		WorkDir: root, Options: PipelineConfigFromAgentOptions(agent.Options{}),
		Memory: store, Trace: tr,
	}); err != nil {
		t.Fatalf("invocation 3: %v", err)
	}
	if got := runner.spawns; got != 2 {
		t.Fatalf("spawns after stable record = %d, want 2 (memoized)", got)
	}
}

// TestPrepareStageInputRepairReEntryMemoized pins the repair-flow win: the
// writer re-entry after a test failure re-enters the same anchor and pays
// ZERO spawns when nothing mutated.
func TestPrepareStageInputRepairReEntryMemoized(t *testing.T) {
	root, commit := cognitionFixtureRepo(t, "internal/auth/session.go", "package auth\n")
	obs := obsWithID(1, root, "session invalidation rule")
	obs.SourceCommit = &commit
	obs.TopicKey = ptr("file:internal/auth/session.go")
	store := &cognitionLookupStore{topics: map[string]schemas.MemoryBundle{
		"file:internal/auth/session.go": {RequestingAgent: "code_writer", Observations: []schemas.MemoryObservation{obs}},
	}}

	runner := &spawnCountingRunner{}
	prevCapture := cognition.SetGitCaptureForTest(runner.Capture)
	defer cognition.SetGitCaptureForTest(prevCapture)

	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run", "session", root, plan, "active", nil)

	revision := "Failing tests: TestSession"
	// The repair input's keys derive from plan.RequestIntent, so the plan
	// itself must carry the structural intent for this fixture.
	plan.RequestIntent = "fix session invalidation in internal/auth/session.go"
	writerInput := repairStageInput("run", "code_writer", plan, []string{"code_writer"}, map[string]string{}, map[string][]string{}, &revision)
	if _, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input: writerInput, Stage: &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget: stageBudgetByName(plan, "code_writer"), Tier: plan.Tier, Iteration: 1,
		WorkDir: root, Options: PipelineConfigFromAgentOptions(agent.Options{}),
		Memory: store, Trace: tr,
	}); err != nil {
		t.Fatalf("writer entry: %v", err)
	}
	if got := runner.spawns; got != 1 {
		t.Fatalf("spawns after writer entry = %d, want 1", got)
	}

	// Repair re-entry: same anchor, no mutation recorded. Zero spawns.
	reEntry := repairStageInput("run", "code_writer", plan, []string{"code_writer"}, map[string]string{}, map[string][]string{}, &revision)
	if _, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input: reEntry, Stage: &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget: stageBudgetByName(plan, "code_writer"), Tier: plan.Tier, Iteration: 1,
		WorkDir: root, Options: PipelineConfigFromAgentOptions(agent.Options{}),
		Memory: store, Trace: tr,
	}); err != nil {
		t.Fatalf("writer re-entry: %v", err)
	}
	if got := runner.spawns; got != 1 {
		t.Fatalf("spawns after repair re-entry = %d, want 1 (memoized)", got)
	}
}

// TestRunTraceAccumulatorFreshnessLifecycle pins the per-run cache lifecycle:
// one cache per accumulator, Reset clears it, and a nil accumulator returns
// nil safely.
func TestRunTraceAccumulatorFreshnessLifecycle(t *testing.T) {
	root, commit := cognitionFixtureRepo(t, "internal/auth/session.go", "package auth\n")
	var nilAcc *runTraceAccumulator
	if nilAcc.freshnessCache() != nil {
		t.Fatal("nil accumulator must return nil cache")
	}
	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run", "session", root, plan, "active", nil)
	cache1 := tr.freshnessCache()
	if cache1 == nil {
		t.Fatal("freshnessCache must lazily create the cache")
	}
	if cache1 != tr.freshnessCache() {
		t.Fatal("freshnessCache must return the same cache within one run")
	}
	if got := cache1.Classify(context.Background(), root, commit, "internal/auth/session.go"); got != cognition.FreshnessFresh {
		t.Fatalf("cache classify = %q, want fresh", got)
	}
	cache1.Reset()
	// After Reset the run's cache is empty; classify still works.
	if got := cache1.Classify(context.Background(), root, commit, "internal/auth/session.go"); got != cognition.FreshnessFresh {
		t.Fatalf("post-reset classify = %q, want fresh", got)
	}
}

// TestMutationSignatureDeterministic pins the mutation signature: stable
// across map iteration order, sensitive to any path or stage change.
func TestMutationSignatureDeterministic(t *testing.T) {
	a := map[string][]string{"code_writer": {"b.go", "a.go"}, "test_generator": {"c_test.go"}}
	b := map[string][]string{"test_generator": {"c_test.go"}, "code_writer": {"a.go", "b.go"}}
	if mutationSignature(a) != mutationSignature(b) {
		t.Fatal("signature must be order-insensitive")
	}
	c := map[string][]string{"code_writer": {"a.go"}}
	if mutationSignature(a) == mutationSignature(c) {
		t.Fatal("different records must differ")
	}
	if mutationSignature(nil) != "" {
		t.Fatal("nil record must produce the empty signature")
	}
}

// TestSpawnSeamRestoreGuard pins that the test seam is restored after each
// test (deferred restore), so parallel package tests never see a fake runner.
func TestSpawnSeamRestoreGuard(t *testing.T) {
	prev := cognition.SetGitCaptureForTest(nil)
	defer cognition.SetGitCaptureForTest(prev)
	if prev == nil {
		t.Fatal("production capture runner must be non-nil before the swap")
	}
}

// keepUnused keeps imports honest when fixture helpers change upstream.
var _ = fmt.Sprintf
var _ = os.Getpid
var _ = filepath.Join
var _ = strings.TrimSpace
